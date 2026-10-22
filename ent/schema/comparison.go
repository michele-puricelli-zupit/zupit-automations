package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/zupit-it/zupit-automations/internal/model"
)

type Comparison struct{ ent.Schema }

func (Comparison) Fields() []ent.Field {
	return []ent.Field{
		field.Int("run_id").Unique(),
		field.String("status").Default("pending"),
		field.Int("attempts").Default(0),
		field.Time("next_at").Default(time.Now),
		field.JSON("result", model.ComparisonResult{}),
		field.String("error").Default(""),
		field.String("reference").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
