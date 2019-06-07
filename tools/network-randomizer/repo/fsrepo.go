package repo

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	badgerds "github.com/ipfs/go-ds-badger"
	lockfile "github.com/ipfs/go-fs-lock"
	keystore "github.com/ipfs/go-ipfs-keystore"
	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"

	"github.com/filecoin-project/go-filecoin/tools/network-randomizer/config"
)

const (
	// apiFile is the filename containing the fcnr node's api address.
	apiFile            = "api"
	configFilename     = "config.json"
	tempConfigFilename = ".config.json.temp"
	lockFile           = "repo.lock"
	versionFilename    = "version"

	// DefaultRepoDir is the default directory of the fcnr repo
	DefaultRepoDir = "repo"
)

// FSRepo is a repo implementation backed by a filesystem.
type FSRepo struct {
	// Path to the repo root directory.
	path    string
	version uint

	// lk protects the config file
	lk  sync.RWMutex
	cfg *config.Config

	ds       Datastore
	keystore keystore.Keystore

	// lockfile is the file system lock to prevent others from opening the same repo.
	lockfile io.Closer
}

var _ Repo = (*FSRepo)(nil)

// InitFSRepo initializes a new repo at a target path, establishing a provided configuration.
// The target path must not exist, or must reference an empty, writable directory.
func InitFSRepo(targetPath string, cfg *config.Config) error {
	repoPath, err := homedir.Expand(targetPath)
	if err != nil {
		return err
	}

	if err := ensureWritableDirectory(repoPath); err != nil {
		return errors.Wrap(err, "no writable directory")
	}

	empty, err := isEmptyDir(repoPath)
	if err != nil {
		return errors.Wrapf(err, "failed to list repo directory %s", repoPath)
	}
	if !empty {
		return fmt.Errorf("refusing to initialize repo in non-empty directory %s", repoPath)
	}

	if err := WriteVersion(repoPath, Version); err != nil {
		return errors.Wrap(err, "initializing repo version failed")
	}

	if err := initConfig(repoPath, cfg); err != nil {
		return errors.Wrap(err, "initializing config file failed")
	}
	return nil
}

// OpenFSRepo opens an already initialized fsrepo at the given path
func OpenFSRepo(repoPath string) (*FSRepo, error) {
	repoPath, err := homedir.Expand(repoPath)
	if err != nil {
		return nil, err
	}

	hasConfig, err := hasConfig(repoPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to check for repo config")
	}

	if !hasConfig {
		return nil, errors.Errorf("no repo found at %s; run: 'fcnr init [--repodir=%s]'", repoPath, repoPath)
	}

	r := &FSRepo{path: repoPath}

	r.lockfile, err = lockfile.Lock(r.path, lockFile)
	if err != nil {
		return nil, errors.Wrap(err, "failed to take repo lock")
	}

	if err := r.loadFromDisk(); err != nil {
		_ = r.lockfile.Close()
		return nil, err
	}

	return r, nil
}

// Config returns the configuration object.
func (r *FSRepo) Config() *config.Config {
	r.lk.RLock()
	defer r.lk.RUnlock()

	return r.cfg
}

// ReplaceConfig replaces the current config with the newly passed in one.
func (r *FSRepo) ReplaceConfig(cfg *config.Config) error {
	r.lk.Lock()
	defer r.lk.Unlock()

	r.cfg = cfg
	tmp := filepath.Join(r.path, tempConfigFilename)
	err := os.RemoveAll(tmp)
	if err != nil {
		return err
	}
	err = r.cfg.WriteFile(tmp)
	if err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(r.path, configFilename))
}

// Datastore returns the datastore.
func (r *FSRepo) Datastore() Datastore {
	return r.ds
}

// Keystore returns the keystore
func (r *FSRepo) Keystore() keystore.Keystore {
	return r.keystore
}

// SetAPIAddr writes the address to the API file. SetAPIAddr expects parameter
// `port` to be of the form `:<port>`.
func (r *FSRepo) SetAPIAddr(maddr string) error {
	f, err := os.Create(filepath.Join(r.path, apiFile))
	if err != nil {
		return errors.Wrap(err, "could not create API file")
	}

	defer f.Close() // nolint: errcheck

	_, err = f.WriteString(maddr)
	if err != nil {
		// If we encounter an error writing to the API file,
		// delete the API file. The error encountered while
		// deleting the API file will be returned (if one
		// exists) instead of the write-error.
		if err := r.removeAPIFile(); err != nil {
			return errors.Wrap(err, "failed to remove API file")
		}

		return errors.Wrap(err, "failed to write to API file")
	}

	return nil
}

// APIAddr reads the FSRepo's api file and returns the api address
func (r *FSRepo) APIAddr() (string, error) {
	return apiAddrFromFile(filepath.Join(filepath.Clean(r.path), apiFile))
}

// APIAddrFromFile reads the address from the API file at the given path.
// A relevant comment from a similar function at go-ipfs/repo/fsrepo/fsrepo.go:
// This is a concurrent operation, meaning that any process may read this file.
// Modifying this file, therefore, should use "mv" to replace the whole file
// and avoid interleaved read/writes
func apiAddrFromFile(apiFilePath string) (string, error) {
	contents, err := ioutil.ReadFile(apiFilePath)
	if err != nil {
		return "", errors.Wrap(err, "failed to read API file")
	}

	return string(contents), nil
}

// Version returns the version of the repo
func (r *FSRepo) Version() uint {
	return r.version
}

// Path returns the path the fsrepo is at
func (r *FSRepo) Path() (string, error) {
	return r.path, nil
}

// Close closes the repo.
func (r *FSRepo) Close() error {
	if err := r.ds.Close(); err != nil {
		return errors.Wrap(err, "failed to close datastore")
	}

	if err := r.removeAPIFile(); err != nil {
		return errors.Wrap(err, "error removing API file")
	}

	return r.lockfile.Close()
}

// APIAddrFromRepoPath returns the api addr from the filecoin repo
func APIAddrFromRepoPath(repoPath string) (string, error) {
	repoPath, err := homedir.Expand(repoPath)
	if err != nil {
		return "", errors.Wrap(err, fmt.Sprintf("can't resolve local repo path %s", repoPath))
	}
	return apiAddrFromFile(filepath.Join(repoPath, apiFile))
}

func (r *FSRepo) removeAPIFile() error {
	return r.removeFile(filepath.Join(r.path, apiFile))
}

func (r *FSRepo) removeFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// Tests whether a repo directory contains the expected config file.
func hasConfig(p string) (bool, error) {
	configPath := filepath.Join(p, configFilename)

	_, err := os.Lstat(configPath)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}

func (r *FSRepo) loadFromDisk() error {
	localVersion, err := r.loadVersion()
	if err != nil {
		return errors.Wrap(err, "failed to load version")
	}

	if localVersion < Version {
		panic("bad version")
	}

	if localVersion > Version {
		return fmt.Errorf("binary needs update to handle repo version, got %d expected %d. Update binary to latest release", localVersion, Version)
	}

	r.version = localVersion

	if err := r.loadConfig(); err != nil {
		return errors.Wrap(err, "failed to load config file")
	}

	if err := r.openDatastore(); err != nil {
		return errors.Wrap(err, "failed to open datastore")
	}

	if err := r.openKeystore(); err != nil {
		return errors.Wrap(err, "failed to open keystore")
	}

	return nil
}

func (r *FSRepo) loadVersion() (uint, error) {
	// TODO: limited file reading, to avoid attack vector
	file, err := ioutil.ReadFile(filepath.Join(r.path, versionFilename))
	if err != nil {
		return 0, err
	}

	version, err := strconv.Atoi(strings.Trim(string(file), "\n"))
	if err != nil {
		return 0, errors.New("corrupt version file: version is not an integer")
	}

	return uint(version), nil
}

func (r *FSRepo) loadConfig() error {
	configFile := filepath.Join(r.path, configFilename)

	cfg, err := config.ReadFile(configFile)
	if err != nil {
		return errors.Wrapf(err, "failed to read config file at %q", configFile)
	}

	r.cfg = cfg
	return nil
}

func (r *FSRepo) openKeystore() error {
	ksp := filepath.Join(r.path, "keystore")

	ks, err := keystore.NewFSKeystore(ksp)
	if err != nil {
		return err
	}

	r.keystore = ks

	return nil
}

func (r *FSRepo) openDatastore() error {
	switch r.cfg.Datastore.Type {
	case "badgerds":
		ds, err := badgerds.NewDatastore(filepath.Join(r.path, r.cfg.Datastore.Path), badgerOptions())
		if err != nil {
			return err
		}
		r.ds = ds
	default:
		return fmt.Errorf("unknown datastore type in config: %s", r.cfg.Datastore.Type)
	}

	return nil
}

func badgerOptions() *badgerds.Options {
	result := &badgerds.DefaultOptions
	result.Truncate = true
	return result
}

// Ensures that path points to a read/writable directory, creating it if necessary.
func ensureWritableDirectory(path string) error {
	// Attempt to create the requested directory, accepting that something might already be there.
	err := os.Mkdir(path, 0775)

	if err == nil {
		return nil // Skip the checks below, we just created it.
	} else if !os.IsExist(err) {
		return errors.Wrapf(err, "failed to create directory %s", path)
	}

	// Inspect existing directory.
	stat, err := os.Stat(path)
	if err != nil {
		return errors.Wrapf(err, "failed to stat path \"%s\"", path)
	}
	if !stat.IsDir() {
		return errors.Errorf("%s is not a directory", path)
	}
	if (stat.Mode() & 0600) != 0600 {
		return errors.Errorf("insufficient permissions for path %s, got %04o need %04o", path, stat.Mode(), 0600)
	}
	return nil
}

// Tests whether the directory at path is empty
func isEmptyDir(path string) (bool, error) {
	infos, err := ioutil.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(infos) == 0, nil
}

// WriteVersion writes the given version to the repo version file.
func WriteVersion(p string, version uint) error {
	return ioutil.WriteFile(filepath.Join(p, versionFilename), []byte(strconv.Itoa(int(version))), 0644)
}

func initConfig(p string, cfg *config.Config) error {
	configFile := filepath.Join(p, configFilename)
	if fileExists(configFile) {
		return fmt.Errorf("file already exists: %s", configFile)
	}

	return cfg.WriteFile(configFile)
}

func fileExists(file string) bool {
	_, err := os.Stat(file)
	if os.IsNotExist(err) {
		return false
	}
	return err == nil
}