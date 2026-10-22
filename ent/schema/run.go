package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/zupit-it/zupit-automations/internal/model"
	"time"
)

type Run struct{ ent.Schema }

func (Run) Fields() []ent.Field {
	return []ent.Field{
		field.Int("flow_id"),
		field.Int("inbox_id"),
		field.Int("source_id"),
		field.String("flow_name"),
		field.String("message_id"),
		field.String("subject"),
		field.JSON("snapshot", model.Snapshot{}),
		field.String("status").Default("pending"),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
func (Run) Indexes() []ent.Index { return []ent.Index{index.Fields("flow_id", "inbox_id").Unique()} }
