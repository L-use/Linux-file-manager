package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

const favoritesFile = "favorites.json"

func registerFavorites(register func(string, bool, func(http.ResponseWriter, *http.Request) error), s *fileStore, state string) {
	var mu sync.Mutex
	name := filepath.Join(state, favoritesFile)
	read := func() (map[string][]string, error) {
		all := map[string][]string{}
		data, err := os.ReadFile(name)
		if errors.Is(err, fs.ErrNotExist) {
			return all, nil
		}
		if err != nil {
			return nil, err
		}
		err = json.Unmarshal(data, &all)
		if all == nil {
			all = map[string][]string{}
		}
		return all, err
	}
	register("GET /api/favorites", true, func(w http.ResponseWriter, r *http.Request) error {
		mu.Lock()
		defer mu.Unlock()
		all, err := read()
		if err != nil {
			return err
		}
		items := all[s.absolute]
		if items == nil {
			items = []string{}
		}
		return respond(w, items)
	})
	register("POST /api/favorites", true, func(w http.ResponseWriter, r *http.Request) error {
		var input struct {
			Path   string
			Remove bool
		}
		if err := parseJSON(w, r, &input); err != nil {
			return err
		}
		if err := s.checked(input.Path, false); err != nil {
			return err
		}
		if !input.Remove {
			dir, err := s.dir(input.Path)
			if err != nil {
				return err
			}
			dir.Close()
		}
		mu.Lock()
		defer mu.Unlock()
		all, err := read()
		if err != nil {
			return err
		}
		items := []string{}
		exists := false
		for _, item := range all[s.absolute] {
			if item == input.Path {
				exists = true
				if input.Remove {
					continue
				}
			}
			items = append(items, item)
		}
		if !input.Remove && !exists {
			if len(items) >= 200 {
				return failure(400, "favoritesLimit")
			}
			items = append(items, input.Path)
		}
		all[s.absolute] = items
		data, err := json.Marshal(all)
		if err != nil {
			return err
		}
		if err = writePrivateSetting(name, data, 0600); err != nil {
			return err
		}
		return respond(w, items)
	})
}
