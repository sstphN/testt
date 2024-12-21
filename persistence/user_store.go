// persistence/user_store.go
package persistence

import (
	"encoding/json"
	"os"
	"sync"

	"1233/internal/bots"
)

type UserStore struct {
	FilePath string
	Users    map[int64]*bots.UserSettings
	Mu       sync.Mutex
}

func NewUserStore(filePath string) *UserStore {
	return &UserStore{
		FilePath: filePath,
		Users:    make(map[int64]*bots.UserSettings),
	}
}

func (us *UserStore) Load() error {
	us.Mu.Lock()
	defer us.Mu.Unlock()

	file, err := os.Open(us.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			us.Users = make(map[int64]*bots.UserSettings)
			return nil
		}
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	return decoder.Decode(&us.Users)
}

func (us *UserStore) Save() error {
	us.Mu.Lock()
	defer us.Mu.Unlock()

	file, err := os.Create(us.FilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(us.Users)
}

func (us *UserStore) Set(userID int64, s bots.UserSettings) {
	us.Mu.Lock()
	defer us.Mu.Unlock()
	us.Users[userID] = &s
}

func (us *UserStore) All() map[int64]*bots.UserSettings {
	us.Mu.Lock()
	defer us.Mu.Unlock()
	copy := make(map[int64]*bots.UserSettings)
	for k, v := range us.Users {
		copy[k] = v
	}
	return copy
}
