package dashboard

import (
	"encoding/json"
	"testing"
)

func TestCreationPickerUsesConfiguredIssueTypeWithoutHidingOtherChoices(t *testing.T) {
	for _, requested := range []string{"Task", "task", "10002"} {
		t.Run(requested, func(t *testing.T) {
			m := creationModel(t)
			m.options.SettingValues = map[string]string{"default_issue_type": requested}
			m.options.Actions.CreateTypes = func(string) (map[string]json.RawMessage, error) {
				return map[string]json.RawMessage{
					"10001": json.RawMessage(`{"id":"10001","name":"Bug"}`),
					"10002": json.RawMessage(`{"id":"10002","name":"Task"}`),
				}, nil
			}
			applyCommand(m, press(m, 'n'))
			chooseCreation(m, "DEMO")
			picker := m.dialogs.Top().(*pickerDialog)
			if picker.value() != "10002" || len(picker.choices) != 2 || picker.menu.Query() != "" {
				t.Fatal("configured issue type was not selected with other choices retained")
			}
			chooseCreation(m, "Bug")
			if m.creationEditor == nil || m.creation.typeID != "10001" {
				t.Fatal("explicit choice could not override the configured type")
			}
		})
	}
}

func TestCreationPickerUnavailableOrAmbiguousDefaultRetainsExplicitChoices(t *testing.T) {
	for _, requested := range []string{"missing", "Task"} {
		t.Run(requested, func(t *testing.T) {
			m := creationModel(t)
			m.options.SettingValues = map[string]string{"default_issue_type": requested}
			m.options.Actions.CreateTypes = func(string) (map[string]json.RawMessage, error) {
				return map[string]json.RawMessage{
					"10001": json.RawMessage(`{"id":"10001","name":"Bug"}`),
					"10002": json.RawMessage(`{"id":"10002","name":"Task"}`),
					"10003": json.RawMessage(`{"id":"10003","name":"Task"}`),
				}, nil
			}
			applyCommand(m, press(m, 'n'))
			chooseCreation(m, "DEMO")
			picker := m.dialogs.Top().(*pickerDialog)
			if picker.value() != "10001" || len(picker.choices) != 3 || picker.menu.Query() != "" || m.creationEditor != nil {
				t.Fatal("unavailable or ambiguous default hid choices or bypassed explicit selection")
			}
		})
	}
}
