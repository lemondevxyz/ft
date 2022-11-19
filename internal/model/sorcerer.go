package model

import "sync"

type sorcerer struct {
	mtx   sync.RWMutex
	index int
	slice Collection
}

func (s *sorcerer) setIndex(i int) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.index = i
}

func (s *sorcerer) setSlice(v Collection) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.slice = v
}

func (s *sorcerer) getIndex() int {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.index
}

func (s *sorcerer) getSlice() Collection {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.slice
}
