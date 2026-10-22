package examples

import (
	"embed"
	"encoding/json"
	"errors"
	"github.com/zupit-it/zupit-automations/internal/model"
)

//go:embed *.json
var files embed.FS

func Preset(name string) (model.Flow, error) {
	switch name {
	case "srt", "cup", "kit", "support":
	default:
		return model.Flow{}, errors.New("modello non disponibile")
	}
	b, err := files.ReadFile(name + ".json")
	if err != nil {
		return model.Flow{}, err
	}
	var p struct {
		Name       string           `json:"name"`
		Definition model.Definition `json:"definition"`
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return model.Flow{}, err
	}
	return model.Flow{Name: p.Name, Definition: p.Definition}, nil
}
