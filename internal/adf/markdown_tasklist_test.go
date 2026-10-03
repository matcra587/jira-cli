package adf_test

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// GFM task-list items author real ADF taskList/taskItem nodes: state from
// the checkbox, generated localIds, and the result passes schema
// validation end to end.
func TestFromMarkdownAuthorsTaskList(t *testing.T) {
	doc, warnings := mdConvert(t, "- [ ] buy milk\n- [x] drink milk\n")
	mdAssertNoWarnings(t, warnings)

	if kinds := mdKinds(doc); len(kinds) != 1 || kinds[0] != "taskList" {
		t.Fatalf("top-level nodes = %v, want a single taskList", kinds)
	}
	listNode := mdBlock(t, doc, 0)
	list, err := adf.AsTaskList(listNode)
	if err != nil {
		t.Fatalf("AsTaskList: %v", err)
	}
	if list.Attrs.LocalID == "" {
		t.Error("taskList must carry a generated localId")
	}

	items := listNode.Content()
	if len(items) != 2 {
		t.Fatalf("expected 2 task items, got %d", len(items))
	}
	for i, wantState := range []string{"TODO", "DONE"} {
		item, err := adf.AsTaskItem(&items[i])
		if err != nil {
			t.Fatalf("item %d: AsTaskItem: %v", i, err)
		}
		if item.Attrs.State != wantState {
			t.Errorf("item %d state = %q, want %q", i, item.Attrs.State, wantState)
		}
		if item.Attrs.LocalID == "" {
			t.Errorf("item %d missing generated localId", i)
		}
	}
	mdValidate(t, doc)
}

// A nested pure task list nests as a taskList sibling inside the parent
// taskList — ADF's shape for indented action items.
func TestFromMarkdownNestsTaskLists(t *testing.T) {
	doc, warnings := mdConvert(t, "- [ ] parent\n  - [x] child\n")
	mdAssertNoWarnings(t, warnings)

	list := mdBlock(t, doc, 0)
	children := list.Content()
	if list.Type() != "taskList" || len(children) != 2 {
		t.Fatalf("expected taskList with taskItem + nested taskList, got %q with %d children", list.Type(), len(children))
	}
	if children[0].Type() != "taskItem" || children[1].Type() != "taskList" {
		t.Fatalf("expected [taskItem, taskList], got [%s, %s]", children[0].Type(), children[1].Type())
	}

	nested := children[1].Content()
	if len(nested) != 1 || nested[0].Type() != "taskItem" {
		t.Fatalf("nested taskList should hold one taskItem, got %+v", nested)
	}
	item, err := adf.AsTaskItem(&nested[0])
	if err != nil {
		t.Fatalf("AsTaskItem: %v", err)
	}
	if item.Attrs.State != "DONE" {
		t.Errorf("nested item state = %q, want DONE", item.Attrs.State)
	}
	mdValidate(t, doc)
}

// A mixed list (checkbox and plain items) stays a bulletList — ADF forbids
// taskItem outside a pure taskList — with the checkboxes degraded to
// literal text and one non-lossy downgrade warning.
func TestFromMarkdownMixedListDegradesCheckboxes(t *testing.T) {
	doc, warnings := mdConvert(t, "- [ ] boxed\n- plain\n")
	if kinds := mdKinds(doc); len(kinds) != 1 || kinds[0] != "bulletList" {
		t.Fatalf("mixed list must stay a bulletList, got %v", kinds)
	}
	for _, w := range warnings {
		if w.Lossy {
			t.Errorf("mixed-list degrade must be non-lossy, got lossy warning %+v", w)
		}
	}
	if len(warnings) != 1 {
		t.Errorf("expected exactly one downgrade warning, got %d: %+v", len(warnings), warnings)
	}

	// The checkbox survives as literal text in the item's paragraph. 0.15
	// asserted this through its ADF→Markdown renderer, which 0.16 does not
	// have; the ADF text node is the direction-correct assertion anyway.
	if got := mdChild(t, mdBlock(t, doc, 0), 0, 0, 0).Text(); got != "[ ] " {
		t.Errorf("checkbox should degrade to literal text, got %q", got)
	}
	mdValidate(t, doc)
}

// Block content Markdown puts inside a task item that ADF cannot nest
// there (a code block) hoists after the list with a non-lossy downgrade —
// content moves, never vanishes.
func TestTaskItemBlockContentHoists(t *testing.T) {
	doc, warnings := mdConvert(t, "- [ ] check output\n\n  ```\n  go test\n  ```\n")
	kinds := mdKinds(doc)
	if len(kinds) != 2 || kinds[0] != "taskList" || kinds[1] != "codeBlock" {
		t.Fatalf("expected [taskList, codeBlock (hoisted)], got %v", kinds)
	}
	for _, w := range warnings {
		if w.Lossy {
			t.Errorf("hoisting is a downgrade, not a loss: %+v", w)
		}
	}
	mdValidate(t, doc)
}
