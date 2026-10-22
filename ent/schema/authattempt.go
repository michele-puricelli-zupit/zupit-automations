package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"time"
)

type AuthAttempt struct{ ent.Schema }

func (AuthAttempt) Fields() []ent.Field {
	return []ent.Field{
		field.String("state_hash").Unique(),
		field.String("nonce"),
		field.String("verifier").Sensitive(),
		field.String("purpose"),
		field.Int("connection_id").Default(0),
		field.Int("user_id").Default(0),
		field.String("binding_hash"),
		field.Time("expires_at"),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
