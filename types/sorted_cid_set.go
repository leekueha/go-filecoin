package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	cbor "gx/ipfs/QmPbqRavwDZLfmpeW6eoyAoQ5rT2LoCW98JhvRc22CqkZS/go-ipld-cbor"
	cid "gx/ipfs/QmYVNvtQkeZ6AKSwDrjQTs432QtL6umrrK41EBq3cu7iSP/go-cid"
	"gx/ipfs/QmcrriCMhjb5ZWzmPNxmP53px47tSPcXBNaMtLdgcKFJYk/refmt/obj/atlas"
)

func init() {
	cbor.RegisterCborType(atlas.BuildEntry(SortedCidSet{}).Transform().
		TransformMarshal(atlas.MakeMarshalTransformFunc(
			func(s SortedCidSet) ([]*cid.Cid, error) {
				return s.s, nil
			})).
		TransformUnmarshal(atlas.MakeUnmarshalTransformFunc(
			func(s []*cid.Cid) (SortedCidSet, error) {
				for i := 0; i < len(s)-1; i++ {
					// Note that this will also catch duplicates.
					if !cidLess(s[i], s[i+1]) {
						return SortedCidSet{}, fmt.Errorf(
							"invalid serialization of SortedCidSet - %s not less than %s", s[i].String(), s[i+1].String())
					}
				}
				return SortedCidSet{s: s}, nil
			})).
		Complete())
}

// SortedCidSet is a set of Cids that is maintained sorted. The externally visible effect as
// compared to cid.Set is that iteration is cheap and always in-order.
// Sort order is lexicographic ascending, by serialization of the cid.
// TODO: This should probably go into go-cid package - see https://github.com/ipfs/go-cid/issues/45.
type SortedCidSet struct {
	s []*cid.Cid // should be maintained sorted
}

// NewSortedCidSet returns a SortedCidSet with the specified items.
func NewSortedCidSet(ids ...*cid.Cid) (res SortedCidSet) {
	for _, id := range ids {
		res.Add(id)
	}
	return
}

// Add adds a cid to the set. Returns true if the item was added (didn't already exist), false
// otherwise.
func (s *SortedCidSet) Add(id *cid.Cid) bool {
	idx := s.search(id)
	if idx < len(s.s) && s.s[idx].Equals(id) {
		return false
	}
	s.s = append(s.s, nil)
	copy(s.s[idx+1:], s.s[idx:])
	s.s[idx] = id
	return true
}

// Has returns true if the set contains the specified cid.
func (s SortedCidSet) Has(id *cid.Cid) bool {
	idx := s.search(id)
	return idx < len(s.s) && s.s[idx].Equals(id)
}

// Len returns the number of items in the set.
func (s SortedCidSet) Len() int {
	return len(s.s)
}

// Empty returns true if the set is empty.
func (s SortedCidSet) Empty() bool {
	return s.Len() == 0
}

// Remove removes a cid from the set. Returns true if the item was removed (did in fact exist in
// the set), false otherwise.
func (s *SortedCidSet) Remove(id *cid.Cid) bool {
	idx := s.search(id)
	if idx < len(s.s) && s.s[idx].Equals(id) {
		copy(s.s[idx:], s.s[idx+1:])
		s.s = s.s[0 : len(s.s)-1]
		return true
	}
	return false
}

// Clear removes all entries from the set.
func (s *SortedCidSet) Clear() {
	s.s = s.s[:0]
}

// Iter returns an iterator that allows the caller to iterate the set in its sort order.
func (s SortedCidSet) Iter() sortedCidSetIterator { // nolint
	return sortedCidSetIterator{
		s: s.s,
		i: 0,
	}
}

// Equals returns true if the set contains the same items as another set.
func (s SortedCidSet) Equals(s2 SortedCidSet) bool {
	if s.Len() != s2.Len() {
		return false
	}

	i1 := s.Iter()
	i2 := s2.Iter()

	for i := 0; i < s.Len(); i++ {
		if !i1.Value().Equals(i2.Value()) {
			return false
		}
	}

	return true
}

// String returns a string listing the cids in the set.
func (s SortedCidSet) String() string {
	out := "{"
	for it := s.Iter(); !it.Complete(); it.Next() {
		out = fmt.Sprintf("%s %s", out, it.Value().String())
	}
	return out + " }"
}

// ToSlice returns a slice listing the cids in the set.
func (s SortedCidSet) ToSlice() []*cid.Cid {
	out := make([]*cid.Cid, s.Len())
	var i int
	for it := s.Iter(); !it.Complete(); it.Next() {
		out[i] = it.Value()
		i++
	}
	return out
}

// MarshalJSON serializes the set to JSON.
func (s SortedCidSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.s)
}

// UnmarshalJSON parses JSON into the set.
func (s *SortedCidSet) UnmarshalJSON(b []byte) error {
	var ts []*cid.Cid
	if err := json.Unmarshal(b, &ts); err != nil {
		return err
	}
	for i := 0; i < len(ts)-1; i++ {
		if !cidLess(ts[i], ts[i+1]) {
			return fmt.Errorf("invalid input - cids not sorted")
		}
	}
	s.s = ts
	return nil
}

func (s SortedCidSet) search(id *cid.Cid) int {
	return sort.Search(len(s.s), func(i int) bool {
		return !cidLess(s.s[i], id)
	})
}

type sortedCidSetIterator struct {
	s []*cid.Cid
	i int
}

// Complete returns true if the iterator has reached the end of the set.
func (si *sortedCidSetIterator) Complete() bool {
	return si.i >= len(si.s)
}

// Next advances the iterator to the next item and returns true if there is such an item.
func (si *sortedCidSetIterator) Next() bool {
	switch {
	case si.i < len(si.s):
		si.i++
		return si.i < len(si.s)
	case si.i == len(si.s):
		return false
	default:
		panic("unreached")
	}
}

// Value returns the current item for the iterator
func (si sortedCidSetIterator) Value() *cid.Cid {
	switch {
	case si.i < len(si.s):
		return si.s[si.i]
	case si.i == len(si.s):
		return nil
	default:
		panic("unreached")
	}
}

// Note: this relies on knowledge of internal layout of Cid.
// TODO: ideally cid would just implement this. See: https://github.com/ipfs/go-cid/issues/46
func cidLess(c1, c2 *cid.Cid) bool {
	p1 := c1.Prefix()
	p2 := c2.Prefix()
	return p1.Version < p2.Version || p1.Codec < p2.Codec || bytes.Compare(c1.Hash(), c2.Hash()) < 0
}
