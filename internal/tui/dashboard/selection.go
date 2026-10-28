package dashboard

// Selection follows native issue identities, never row offsets. Refresh can
// reorder rows, and a review must bind the same issues the user marked.
func (m *Model) markedKeys() []string {
	keys := []string{}
	for _, issue := range m.issues {
		if m.marks[issue.Key] {
			keys = append(keys, issue.Key)
		}
	}
	return keys
}

func (m *Model) toggleMark() {
	if m.actionBusy || m.detailFocused || m.previewFocused {
		return
	}
	key := m.selectedKey()
	if key == "" {
		return
	}
	if m.marks == nil {
		m.marks = make(map[string]bool)
	}
	if m.marks[key] {
		delete(m.marks, key)
	} else {
		m.marks[key] = true
	}
	m.markAnchor = key
}

func (m *Model) markAll() {
	if m.actionBusy || m.detailFocused || m.previewFocused {
		return
	}
	all := len(m.issues) > 0 && len(m.markedKeys()) == len(m.issues)
	if m.marks == nil {
		m.marks = make(map[string]bool)
	}
	for _, issue := range m.issues {
		if all {
			delete(m.marks, issue.Key)
		} else {
			m.marks[issue.Key] = true
		}
	}
}

func (m *Model) invertMarks() {
	if m.actionBusy || m.detailFocused || m.previewFocused {
		return
	}
	if m.marks == nil {
		m.marks = make(map[string]bool)
	}
	for _, issue := range m.issues {
		if m.marks[issue.Key] {
			delete(m.marks, issue.Key)
		} else {
			m.marks[issue.Key] = true
		}
	}
}

func (m *Model) markRange() {
	if m.actionBusy || m.detailFocused || m.previewFocused {
		return
	}
	cursor, ok := m.rows.Selected()
	if !ok {
		return
	}
	anchor := cursor
	for i, issue := range m.issues {
		if issue.Key == m.markAnchor {
			anchor = i
			break
		}
	}
	if m.marks == nil {
		m.marks = make(map[string]bool)
	}
	if m.markAnchor == "" {
		m.markAnchor = m.issues[cursor].Key
	}
	for i := min(anchor, cursor); i <= max(anchor, cursor); i++ {
		m.marks[m.issues[i].Key] = true
	}
}

func (m *Model) pruneMarks() {
	available := make(map[string]bool, len(m.issues))
	for _, issue := range m.issues {
		available[issue.Key] = true
	}
	for key := range m.marks {
		if !available[key] {
			delete(m.marks, key)
		}
	}
	if !available[m.markAnchor] {
		m.markAnchor = ""
	}
}
