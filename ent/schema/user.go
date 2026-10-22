package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"time"
)

type User struct{ ent.Schema }

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("subject").Unique(),
		field.String("email").Unique(),
		field.String("name"),
		field.Bool("admin").Default(false),
		field.Bool("active").Default(true),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
