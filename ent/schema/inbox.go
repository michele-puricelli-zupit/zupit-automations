package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/zupit-it/zupit-automations/internal/model"
	"time"
)

type Inbox struct{ ent.Schema }

func (Inbox) Fields() []ent.Field {
	return []ent.Field{
		field.Int("source_id"),
		field.String("message_id"),
		field.JSON("message", model.Message{}),
		field.Time("received_at"),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
func (Inbox) Indexes() []ent.Index {
	return []ent.Index{index.Fields("source_id", "message_id").Unique()}
}
