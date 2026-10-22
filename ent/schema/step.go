package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/zupit-it/zupit-automations/internal/model"
	"time"
)

type Step struct{ ent.Schema }

func (Step) Fields() []ent.Field {
	return []ent.Field{
		field.Int("run_id"),
		field.Int("position"),
		field.JSON("action", model.Action{}),
		field.String("status").Default("pending"),
		field.Int("attempts").Default(0),
		field.Time("next_at").Default(time.Now),
		field.String("reference").Default(""),
		field.String("error").Default(""),
		field.Time("started_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
func (Step) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id", "position").Unique(), index.Fields("status", "next_at")}
}
