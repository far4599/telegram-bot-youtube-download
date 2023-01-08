package repository

import (
	lru "github.com/hashicorp/golang-lru"
)

type InMemRepository struct {
	*lru.Cache
}

func NewInMemRepository() (*InMemRepository, error) {
	cache, err := lru.New(10_000)
	if err != nil {
		return nil, err
	}

	return &InMemRepository{
		Cache: cache,
	}, nil
}
