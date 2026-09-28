// Package providerpolicy contains the reviewed external media and ticket domains.
package providerpolicy

import (
	"embed"
	"encoding/json"
)

//go:embed domains.json
var files embed.FS

type Policy struct {
	Images  []string `json:"images"`
	Tickets []string `json:"tickets"`
}

func Defaults() Policy {
	data, err := files.ReadFile("domains.json")
	if err != nil {
		panic(err)
	}
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		panic(err)
	}
	return policy
}
