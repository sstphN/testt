package storage

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"sync"
)

// UserSetting хранит настройки пользователя
type UserSetting struct {
	Mode          string  `json:"mode"`
	Metric        string  `json:"metric"`
	Threshold     float64 `json:"threshold"`
	TimeFrame     string  `json:"timeframe"`
	TargetBot     string  `json:"target_bot"`
	CurrentChange float64 `json:"current_change,omitempty"`
	CurrentPrice  float64 `json:"current_price,omitempty"`
}

// Storage представляет собой хранилище данных пользователей
type Storage struct {
	sync.Mutex
	data     map[int64]UserSetting
	filePath string
}

// NewStorage создаёт новое хранилище и загружает данные из файла, если он существует
func NewStorage(filePath string) *Storage {
	s := &Storage{
		data:     make(map[int64]UserSetting),
		filePath: filePath,
	}
	s.load()
	return s
}

// load загружает данные из JSON-файла
func (s *Storage) load() {
	s.Lock()
	defer s.Unlock()

	if _, err := os.Stat(s.filePath); os.IsNotExist(err) {
		// Файл не существует, создаём пустое хранилище
		s.data = make(map[int64]UserSetting)
		return
	}

	data, err := ioutil.ReadFile(s.filePath)
	if err != nil {
		log.Printf("Не удалось прочитать файл данных: %v", err)
		return
	}

	if err := json.Unmarshal(data, &s.data); err != nil {
		log.Printf("Не удалось десериализовать данные: %v", err)
		return
	}
}

// Save сохраняет текущие данные в JSON-файл
func (s *Storage) Save() error {
	s.Lock()
	defer s.Unlock()

	data, err := json.MarshalIndent(s.data, "", " ")
	if err != nil {
		return err
	}

	return ioutil.WriteFile(s.filePath, data, 0644)
}

// AddUser добавляет нового пользователя с настройками по умолчанию
func (s *Storage) AddUser(userID int64, defaultSetting UserSetting) error {
	s.Lock()
	defer s.Unlock()

	if _, exists := s.data[userID]; exists {
		// Пользователь уже существует
		return nil
	}

	s.data[userID] = defaultSetting
	return s.Save()
}

// UpdateSetting обновляет настройки пользователя
func (s *Storage) UpdateSetting(userID int64, setting UserSetting) error {
	s.Lock()
	defer s.Unlock()

	s.data[userID] = setting
	return s.Save()
}

// GetUserSettings получает настройки пользователя
func (s *Storage) GetUserSettings(userID int64) (UserSetting, bool) {
	s.Lock()
	defer s.Unlock()

	setting, exists := s.data[userID]
	return setting, exists
}

func (s *Storage) GetAllSettings() map[int64]UserSetting {
	s.Lock()
	defer s.Unlock()

	copyData := make(map[int64]UserSetting)
	for k, v := range s.data {
		copyData[k] = v
	}
	return copyData
}
