package envelope

// TUIOutput identifies the admitted dashboard session. Success is the human
// screen itself; machine modes return the ordinary structured refusal.
type TUIOutput struct {
	View string `json:"view"`
}

func init() {
	Register("tui", TUIOutput{}, nil)
}
