package views

import (
	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/model"
)

type Data struct {
	Title, Active, CSRF, Error, Notice string
	User                               model.User
	Connections                        []model.Connection
	Flows                              []model.Flow
	Runs                               []model.Run
	Connection                         model.Connection
	Flow                               model.Flow
	Run                                model.Run
	Raw, Simulation, Branch            string
	JevReady                           bool
	Jira                               map[string]connectors.JiraChoices
}
