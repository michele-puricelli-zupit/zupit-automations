package store

import dbent "github.com/zupit-it/zupit-automations/ent"

func isNotFound(err error) bool { return dbent.IsNotFound(err) }
