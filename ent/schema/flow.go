package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/zupit-it/zupit-automations/internal/model"
	"time"
)

type Flow struct{ ent.Schema }

func (Flow) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.Int("source_id"),
		field.Bool("enabled").Default(false),
		field.JSON("definition", model.Definition{}),
		field.Time("activated_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
