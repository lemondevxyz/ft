package model

import "sync"

type sorcerer struct {
	mtx   sync.RWMutex
	index int
	slice Collection
}

func (s *sorcerer) setIndex(i int) {
	s.mtx.Lock()
	s.index = i
	s.mtx.Unlock()
}

func (s *sorcerer) setSlice(v Collection) {
	s.mtx.Lock()
	s.slice = v
	s.mtx.Unlock()
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
