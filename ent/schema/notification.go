package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"time"
)

type Notification struct{ ent.Schema }

func (Notification) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").Unique(),
		field.String("body"),
		field.Bool("delivered").Default(false),
		field.Int("attempts").Default(0),
		field.Time("next_at").Default(time.Now),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
