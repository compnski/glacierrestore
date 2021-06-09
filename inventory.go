package main

import (
	"encoding/json"
	"io/ioutil"
)

type Archive struct {
	ArchiveId          string
	ArchiveDescription string
	Size               int
	CreationDate       string
	SHA256TreeHash     string
	Path               string
	RestoreJobId       string
}

func (a *Archive) Process() error {
	var desc SerializedArchiveDescription
	err := json.Unmarshal([]byte(a.ArchiveDescription), &desc)
	a.Path = desc.Path
	return err
}

type SerializedArchiveDescription struct {
	Path string `json:"path"`
}

type Inventory struct {
	VaultARN      string
	InventoryDate string
	ArchiveList   []Archive
}

func (i *Inventory) Process() error {
	var err error
	for idx, _ := range i.ArchiveList {
		err = i.ArchiveList[idx].Process()
		if err != nil {
			return err
		}
	}
	return nil
}

func ReadInventoryFile(filePath string) (*Inventory, error) {
	var inventory Inventory
	invJson, err := ioutil.ReadFile(filePath)
	err = json.Unmarshal(invJson, &inventory)
	if err == nil {
		err = inventory.Process()
	}
	if err != nil {
		panic(err)
	}
	return &inventory, err
}
