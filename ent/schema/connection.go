package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"time"
)

type Connection struct{ ent.Schema }

func (Connection) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.Enum("provider").Values("gmail", "jira", "slack"),
		field.JSON("config", map[string]string{}),
		field.Bytes("secret").Sensitive().Optional(),
		field.String("status").Default("pending"),
		field.String("error").Default(""),
		field.String("history_id").Default(""),
		field.Time("watch_expires").Optional().Nillable(),
		field.Time("cutover").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
