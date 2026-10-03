package output

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/x/ansi"
	"github.com/gechr/x/shell"
	"github.com/gechr/x/terminal"
	"golang.org/x/term"
)

// DocumentOptions supplies borrowed streams for interactive paging. The
// command construction owner decides whether a person permits paging.
type DocumentOptions struct {
	Input       io.Reader
	Diagnostics io.Writer
}

// RenderDocument renders through the ordinary human View, preserving the
// destination's color and width, then pages only an overflowing terminal
// document. Machine modes and nonterminal streams retain ordinary Render.
func RenderDocument(ctx context.Context, w io.Writer, format Format, res Result, options DocumentOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, outputFile := w.(*os.File)
	input, inputFile := options.Input.(*os.File)
	if format != FormatHuman || !outputFile || !inputFile || !terminal.Is(file) || !terminal.Is(input) {
		return Render(ctx, w, format, res)
	}
	buffer := &documentBuffer{Buffer: &bytes.Buffer{}, file: file}
	if err := Render(ctx, buffer, format, res); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	content := buffer.String()
	width, height := terminal.Size(file)
	if !documentOverflows(content, width, height) {
		return writeDocument(w, content)
	}
	if err := runPager(ctx, input, file, options.Diagnostics, content); err != nil {
		return &WriteError{Err: err}
	}
	return nil
}

// The buffer retains the actual terminal descriptor so the ordinary View
// measures and styles exactly as it would when drawing to the destination.
type documentBuffer struct {
	*bytes.Buffer
	file *os.File
}

func (b *documentBuffer) Fd() uintptr { return b.file.Fd() }

func writeDocument(w io.Writer, content string) error {
	n, err := io.WriteString(w, content)
	if err != nil {
		return &WriteError{Err: err}
	}
	if n != len(content) {
		return &WriteError{Err: io.ErrShortWrite}
	}
	return nil
}

func documentOverflows(content string, width, height int) bool {
	if width <= 0 || height <= 0 || content == "" {
		return false
	}
	rows := 0
	for line := range strings.SplitSeq(strings.TrimSuffix(content, "\n"), "\n") {
		rows++
		if rows > height {
			return true
		}
		column := 0
		for text := ansi.Strip(line); text != ""; {
			// gechr/x/ansi v0.6.0 exposes total width and a hard wrapper,
			// but its wrapper drops spaces and cannot account for tab stops.
			// Walk the selected width engine's clusters so a wide glyph
			// cannot split across rows. Tabs use conventional eight-column
			// stops, bounded by the right margin; custom stops are unknown.
			cluster, cells := xansi.FirstGraphemeCluster(text, xansi.GraphemeWidth)
			text = text[len(cluster):]
			if cluster == "\t" {
				column = min(width-1, (column/8+1)*8)
				continue
			}
			if cells == 0 {
				continue
			}
			cells = min(cells, width)
			if column+cells > width {
				rows++
				column = 0
			}
			column += cells
			if rows > height {
				return true
			}
		}
	}
	return false
}

func runPager(ctx context.Context, input, destination *os.File, diagnostics io.Writer, content string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	// External pagers may leave raw modes behind, including after cancellation.
	// gechr/x/terminal v0.6.0 provides detection/size but no mode ownership.
	files := []*os.File{input, destination}
	if file, ok := diagnostics.(*os.File); ok {
		files = append(files, file)
	}
	for _, file := range files {
		if !terminal.Is(file) {
			continue
		}
		state, captureErr := term.GetState(int(file.Fd()))
		if captureErr != nil {
			return fmt.Errorf("capture pager terminal: %w", captureErr)
		}
		defer func() {
			if restoreErr := term.Restore(int(file.Fd()), state); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore pager terminal; restore the terminal before continuing: %w", restoreErr))
			}
		}()
	}
	defer func() { err = errors.Join(ctx.Err(), err) }()
	for _, name := range []string{"JIRA_PAGER", "PAGER"} {
		parts, splitErr := shell.Split(strings.TrimSpace(os.Getenv(name)))
		if splitErr != nil || len(parts) == 0 || parts[0] == "" {
			continue
		}
		process := exec.CommandContext(ctx, parts[0], parts[1:]...) //nolint:gosec // the pager is the user's local executable configuration; no shell evaluates it
		process.Stdin = strings.NewReader(content)
		process.Stdout, process.Stderr = destination, diagnostics
		process.WaitDelay = 2 * time.Second
		if startErr := process.Start(); startErr != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			continue
		}
		if waitErr := process.Wait(); waitErr != nil {
			return fmt.Errorf("pager %q failed; use --no-pager to print directly: %w", parts[0], waitErr)
		}
		return nil
	}
	tracked, dest := newTrackingWriter(destination)
	// Root owns SIGINT/SIGTERM through the command context. A second signal
	// handler can block sending to Tea's event loop after context cancellation,
	// preventing its shutdown from restoring the terminal.
	_, err = tea.NewProgram(&documentPager{content: content},
		tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(dest), tea.WithoutSignalHandler()).Run()
	return errors.Join(err, tracked.err)
}

type documentPager struct {
	content string
	view    viewport.Model
	ready   bool
}

func (m *documentPager) Init() tea.Cmd { return nil }

func (m *documentPager) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		if !m.ready {
			m.view = viewport.New(viewport.WithWidth(max(1, message.Width)), viewport.WithHeight(max(1, message.Height-1)))
			m.view.SetContent(m.content)
			m.ready = true
		} else {
			m.view.SetWidth(max(1, message.Width))
			m.view.SetHeight(max(1, message.Height-1))
		}
		return m, nil
	case tea.KeyPressMsg:
		switch message.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "g", "home":
			m.view.GotoTop()
			return m, nil
		case "G", "end":
			m.view.GotoBottom()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.view, cmd = m.view.Update(message)
	return m, cmd
}

func (m *documentPager) View() tea.View {
	if !m.ready {
		return tea.NewView("")
	}
	view := tea.NewView(m.view.View() + "\n(q to quit)")
	view.AltScreen = true
	return view
}
