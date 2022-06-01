package state

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"

	"os"
	"time"
)

type State struct {
	Version            string               `json:"version"`
	ReportLastModified map[string]time.Time `json:"reportLastModified"`

	Periods []BillingPeriod `json:"BillingPeriod"`
}

type Config struct {
	RepositoryPath string
	QueriesPath    string
	BucketName     string
	ReportName     string

	StateFilePath string
}

func Init() *State {
	return &State{
		Version:            "1",
		ReportLastModified: map[string]time.Time{},
	}
}

func Load(config *Config) (*State, error) {
	state := Init()

	if jsonString, err := ioutil.ReadFile(config.StateFilePath); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		if err := json.Unmarshal(jsonString, state); err != nil {
			return nil, err
		}
	}

	if err := os.Mkdir(config.RepositoryPath, 0750); err != nil && !os.IsExist(err) {
		return nil, err
	}

	if err := os.Mkdir(filepath.Join(config.RepositoryPath, "data"), 0750); err != nil && !os.IsExist(err) {
		return nil, err
	}

	if _, err := os.Stat(config.QueriesPath); err != nil {
		return nil, err
	}

	return state, nil
}

func (state *State) Save(config *Config) error {
	if jsonString, err := json.MarshalIndent(state, "", "    "); err != nil {
		return err
	} else {
		if err := ioutil.WriteFile(config.StateFilePath, jsonString, os.ModePerm); err != nil {
			return err
		}
	}

	return nil
}
