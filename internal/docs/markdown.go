package docs

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	clib "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/complete"
	"github.com/gechr/clib/help"
	xslices "github.com/gechr/x/slices"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	"github.com/matcra587/jira-cli/internal/cli/schema"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// GenMarkdownTreeCustom walks cmd and every descendant, writing one Markdown
// page per command into dir as a nested directory tree: a command with
// subcommands becomes <path>/index.md, a leaf becomes <path>.md. The index.md
// naming is what lets Zensical treat the parent command as a clickable section
// index in the navigation (config.py _is_index recognizes index.md / README.md
// only). filePrepender receives the file path and returns front-matter to
// prepend; linkHandler rewrites a target page's relative path into a link. It is
// the entry point used by cmd/gen-docs.
func GenMarkdownTreeCustom(cmd *cobra.Command, dir string, filePrepender, linkHandler func(string) string) (err error) {
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err = GenMarkdownTreeCustom(c, dir, filePrepender, linkHandler); err != nil {
			return err
		}
	}
	path := filepath.Join(dir, filepath.FromSlash(docRelPath(cmd)))
	if err = os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	var f *os.File
	f, err = os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if _, err = io.WriteString(f, filePrepender(path)); err != nil {
		return err
	}
	return GenMarkdownCustom(cmd, f, filePrepender, linkHandler)
}

// docRelPath returns cmd's page path relative to the reference root, slash-
// separated. A command with subcommands becomes a directory with index.md; a
// leaf becomes <name>.md in its parent's directory. The index.md convention is
// what Zensical recognizes as a section index, so the parent command renders as
// a clickable section title in the nav. The path is built from command names
// (no user input), so it cannot contain a "../" escape.
//
//	jira              -> jira/index.md
//	jira issue        -> jira/issue/index.md
//	jira issue create -> jira/issue/create.md
//	jira me           -> jira/me.md
func docRelPath(cmd *cobra.Command) string {
	joined := strings.ReplaceAll(cmd.CommandPath(), " ", "/")
	if len(availableSubcommands(cmd)) > 0 {
		return joined + "/index.md"
	}
	return joined + ".md"
}

// childRelLink returns the link to child c relative to its parent's page, which
// lives in the parent's directory. A child with subcommands lives in its own
// subdirectory (<name>/index.md); a leaf is a sibling file (<name>.md). Zensical
// resolves these relative .md links to the correct site URLs at build time.
func childRelLink(c *cobra.Command) string {
	if len(availableSubcommands(c)) > 0 {
		return c.Name() + "/index.md"
	}
	return c.Name() + ".md"
}

// GenMarkdownCustom renders a single command's page to w. linkHandler rewrites
// references to other generated pages; filePrepender is accepted for signature
// parity with the tree walker and is not used per-command.
func GenMarkdownCustom(cmd *cobra.Command, w io.Writer, _, linkHandler func(string) string) error {
	cmd.InitDefaultHelpFlag()
	cmd.InitDefaultVersionFlag()

	var b bytes.Buffer
	name := cmd.CommandPath()

	// Title and a compact meta block (usage + aliases), in the style of the
	// usage-cli reference pages: `# `jira issue create`` then `- **Usage**: …`.
	fmt.Fprintf(&b, "# `%s`\n\n", name)
	fmt.Fprintf(&b, "- **Usage**: `%s`\n", cmd.UseLine())
	if len(cmd.Aliases) > 0 {
		quoted := xslices.Map(cmd.Aliases, func(a string) string { return "`" + a + "`" })
		fmt.Fprintf(&b, "- **Aliases**: %s\n", strings.Join(quoted, ", "))
	}
	b.WriteString("\n")

	// Prefer the Long description, falling back to Short. Printing both
	// duplicates content because a command's Long typically opens by restating
	// its Short (the common cobra pattern). Long is authored prose; emit it
	// verbatim so intentional Markdown (backtick spans, paragraphs) survives.
	switch {
	case cmd.Long != "":
		fmt.Fprintf(&b, "%s\n\n", cmd.Long)
	case cmd.Short != "":
		fmt.Fprintf(&b, "%s\n\n", cmd.Short)
	}

	writeFlags(&b, cmd)
	writeOutputFields(&b, cmd)
	writeExamples(&b, cmd.Example)
	writeSubcommands(&b, cmd, linkHandler)

	_, err := w.Write(b.Bytes())
	return err
}

// writeFlags renders cmd's flags from clib metadata: local flags grouped by
// their clib group (ordered to match the terminal help via cmdutil.GroupRank),
// then a single "Global flags" block for inherited flags. The help and version
// flags are omitted.
func writeFlags(b *bytes.Buffer, cmd *cobra.Command) {
	metaByName := make(map[string]complete.FlagMeta)
	for _, m := range clib.FlagMeta(cmd) {
		metaByName[m.Name] = m
	}
	defaults := flagDefaults(cmd)

	// Drive the flag layout from clib's help-section model (via the shared
	// cmdutil builder): clib groups the flags, applies jira's task-flow group
	// order, and — by clib's default — hides inherited/global flags on
	// subcommands, so the reference matches `--help` exactly. Each flag's detail
	// (placeholder, allowed values, aliases, hint, negatable, default) is
	// enriched from clib.FlagMeta, which carries fields help.Flag omits.
	for _, section := range cmdutil.StandardHelpSections(cmd) {
		var flags []complete.FlagMeta
		for _, content := range section.Content {
			group, ok := content.(help.FlagGroup)
			if !ok {
				continue // Usage / Examples / subcommand sections render elsewhere.
			}
			for i := range group {
				hf := group[i]
				if hf.Long == "help" || (hf.Long == "" && hf.Short == "h") {
					continue
				}
				if m, ok := metaByName[hf.Long]; ok {
					flags = append(flags, m)
					continue
				}
				// No clib metadata (a plain pflag flag): fall back to the
				// help.Flag the section already carries.
				flags = append(flags, complete.FlagMeta{
					Name:        hf.Long,
					Short:       hf.Short,
					Help:        hf.Desc,
					HasArg:      hf.Placeholder != "",
					Placeholder: hf.Placeholder,
					Enum:        hf.Enum,
					EnumDefault: hf.EnumDefault,
				})
			}
		}
		writeFlagSection(b, section.Title, flags, defaults)
	}
}

// writeFlagSection renders a titled section: one `### ` heading per flag with
// its description, allowed values, default and any aliases. Flags are sorted by
// name for byte-stable output. Nothing is written for an empty section.
func writeFlagSection(b *bytes.Buffer, title string, flags []complete.FlagMeta, defaults map[string]string) {
	if len(flags) == 0 {
		return
	}
	sort.SliceStable(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	fmt.Fprintf(b, "## %s\n\n", title)
	for i := range flags {
		writeFlag(b, flags[i], defaults)
	}
}

// writeFlag renders a single flag entry from its clib metadata.
func writeFlag(b *bytes.Buffer, m complete.FlagMeta, defaults map[string]string) {
	fmt.Fprintf(b, "### `%s`\n\n", flagHeading(m))

	if len(m.Aliases) > 0 {
		quoted := xslices.Map(m.Aliases, func(a string) string { return "`--" + a + "`" })
		fmt.Fprintf(b, "Aliases: %s\n\n", strings.Join(quoted, ", "))
	}

	if m.Help != "" {
		fmt.Fprintf(b, "%s\n\n", escapeText(m.Help))
	}

	if m.ValueHint != "" {
		fmt.Fprintf(b, "Accepts a %s.\n\n", m.ValueHint)
	}

	if m.Negatable {
		prefix := m.InversePrefix
		if prefix == "" {
			prefix = "no-"
		}
		neg := "`--" + prefix + m.Name + "`"
		if m.NegativeDesc != "" {
			fmt.Fprintf(b, "Negate with %s: %s\n\n", neg, escapeText(m.NegativeDesc))
		} else {
			fmt.Fprintf(b, "Negate with %s.\n\n", neg)
		}
	}

	switch {
	case len(m.Enum) > 0:
		writeAllowedValues(b, m)
	case m.HasArg && !m.HideDefault:
		if d := defaults[m.Name]; isMeaningfulDefault(d) {
			fmt.Fprintf(b, "Default: `%s`\n\n", d)
		}
	}
}

// writeAllowedValues renders an enum flag's permitted values as a list, pairing
// each with its short description and marking the default.
func writeAllowedValues(b *bytes.Buffer, m complete.FlagMeta) {
	b.WriteString("Allowed values:\n\n")
	for i, v := range m.Enum {
		line := "- `" + v + "`"
		if i < len(m.EnumTerse) && m.EnumTerse[i] != "" {
			line += " — " + escapeText(m.EnumTerse[i])
		}
		if v == m.EnumDefault {
			line += " (default)"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}

// flagHeading renders a flag's heading, e.g. `-P --project <KEY>`, `--no-input`,
// or `-l --label <NAME>…` for a repeatable flag. The placeholder is the authored
// clib placeholder when set, otherwise the upper-cased flag name. Bool flags
// carry no placeholder. HideShort/HideLong drop the respective form.
func flagHeading(m complete.FlagMeta) string {
	var head string
	if m.Short != "" && !m.HideShort {
		head = "-" + m.Short
	}
	if m.Name != "" && !m.HideLong {
		if head != "" {
			head += " "
		}
		head += "--" + m.Name
	}
	if m.HasArg {
		head += " <" + placeholder(m) + ">"
		if m.IsSlice || m.IsCSV {
			head += "…"
		}
	}
	return head
}

// placeholder returns the value placeholder for a flag: the authored clib
// placeholder when set, otherwise the flag name upper-cased (project -> PROJECT,
// ttl-minutes -> TTL_MINUTES).
func placeholder(m complete.FlagMeta) string {
	if m.PlaceholderOverride && m.Placeholder != "" {
		return m.Placeholder
	}
	return strings.ToUpper(strings.ReplaceAll(m.Name, "-", "_"))
}

// flagDefaults maps every flag name on cmd (local and inherited) to its pflag
// default value, the source for the rendered Default line.
func flagDefaults(cmd *cobra.Command) map[string]string {
	out := make(map[string]string)
	collect := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			if _, ok := out[f.Name]; !ok {
				out[f.Name] = f.DefValue
			}
		})
	}
	collect(cmd.Flags())
	collect(cmd.InheritedFlags())
	return out
}

// isMeaningfulDefault reports whether a default value is worth printing. Empty,
// empty-slice and zero-duration defaults are noise.
func isMeaningfulDefault(d string) bool {
	switch d {
	case "", "[]", "0s":
		return false
	default:
		return true
	}
}

// escapeText makes plain-text flag descriptions safe as Markdown: outside
// backtick code spans it escapes angle brackets (so `<id>` is not eaten as an
// HTML tag) and square brackets (so `[example: …]` is not a broken link
// reference). Text inside code spans is left untouched, where those characters
// are already literal.
func escapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inCode := false
	for _, r := range s {
		if r == '`' {
			inCode = !inCode
			b.WriteRune(r)
			continue
		}
		if !inCode {
			switch r {
			case '<':
				b.WriteString("&lt;")
				continue
			case '>':
				b.WriteString("&gt;")
				continue
			case '[':
				b.WriteString("\\[")
				continue
			case ']':
				b.WriteString("\\]")
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// outputField is one row of the generated "Output fields" table: a dotted/
// bracketed JSON path, its type, and a description.
type outputField struct {
	path string
	typ  string
	desc string
}

// writeOutputFields renders the command's JSON output shape as a table, driven
// by the schema registry that `jira agent schema` emits (internal/cli/schema). Only
// commands with a registered command-specific schema get a table; the rest
// return the standard envelope and render nothing here. Generating the table
// from the registry keeps it in lockstep with the real output — it cannot drift.
func writeOutputFields(b *bytes.Buffer, cmd *cobra.Command) {
	data, ok := schema.OutputSchemaForCommand(cmd)
	if !ok {
		return
	}
	var rows []outputField
	collectOutputFields(data, "", 0, 2, &rows)
	if len(rows) == 0 {
		return
	}
	b.WriteString("## Output fields\n\n")
	b.WriteString("With `--output json`, the response envelope's `data` object carries these fields. Run `jira agent schema` for the full machine-readable schema.\n\n")
	b.WriteString("| Field | Type | Description |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, r := range rows {
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", r.path, escapeTableCell(r.typ), escapeTableCell(r.desc))
	}
	b.WriteString("\n")
}

// collectOutputFields flattens a JSON Schema node into table rows. It descends
// through object properties (dotted paths) and array items (a "[]" path
// segment) until depth reaches maxDepth, where a nested object collapses to a
// single row that lists its child field names. The data root itself (depth 0)
// is not emitted — only its fields are.
func collectOutputFields(node map[string]any, path string, depth, maxDepth int, rows *[]outputField) {
	typ := schemaTypeString(node)

	// Arrays: descend into the element schema, marking the path with "[]". The
	// array and its element are one logical level, so depth is not incremented.
	if strings.Contains(typ, "array") {
		if items, ok := node["items"].(map[string]any); ok {
			collectOutputFields(items, path+"[]", depth, maxDepth, rows)
			return
		}
	}

	// Objects with properties: descend, unless we have hit the depth cap.
	if props, ok := node["properties"].(map[string]any); ok && strings.Contains(typ, "object") {
		if depth < maxDepth {
			for _, name := range sortedSchemaKeys(props) {
				child, _ := props[name].(map[string]any)
				childPath := name
				if path != "" {
					childPath = path + "." + name
				}
				collectOutputFields(child, childPath, depth+1, maxDepth, rows)
			}
			return
		}
		*rows = append(*rows, outputField{path: path, typ: schemaTypeLabel(node), desc: cappedObjectDesc(node, props)})
		return
	}

	// Leaf: scalar, opaque object, or array without a typed element schema.
	*rows = append(*rows, outputField{path: path, typ: schemaTypeLabel(node), desc: schemaDesc(node)})
}

// schemaTypeString returns a node's JSON Schema type, joining a union type
// (e.g. ["object","null"]) with " | ".
func schemaTypeString(node map[string]any) string {
	switch t := node["type"].(type) {
	case string:
		return t
	case []string:
		return strings.Join(t, " | ")
	case []any:
		parts := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " | ")
	}
	return ""
}

// schemaTypeLabel is schemaTypeString plus the JSON Schema "format" hint when
// present, e.g. a date-time string renders as "string (date-time)".
func schemaTypeLabel(node map[string]any) string {
	typ := schemaTypeString(node)
	if f, ok := node["format"].(string); ok && f != "" {
		if typ != "" {
			typ += " (" + f + ")"
		} else {
			typ = f
		}
	}
	return typ
}

// schemaDesc returns a node's description, or the empty string.
func schemaDesc(node map[string]any) string {
	d, _ := node["description"].(string)
	return d
}

// cappedObjectDesc describes a nested object collapsed at the depth cap: its own
// description if it has one, otherwise its child field names (truncated).
func cappedObjectDesc(node, props map[string]any) string {
	if d := schemaDesc(node); d != "" {
		return d
	}
	names := sortedSchemaKeys(props)
	const max = 6
	suffix := ""
	if len(names) > max {
		names = names[:max]
		suffix = ", …"
	}
	return "Fields: " + strings.Join(names, ", ") + suffix
}

// sortedSchemaKeys returns a map's keys sorted, for byte-stable output.
func sortedSchemaKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}

// escapeTableCell makes a value safe inside a Markdown table cell: pipes would
// end the cell, and newlines would break the row.
func escapeTableCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// exampleBlock is one authored example: a comment and the command line(s) it
// describes.
type exampleBlock struct {
	comment string
	cmds    []string
}

// parseExampleBlocks splits an authored Example string (clib's "# comment" then
// "$ command" convention, blocks separated by blank lines) into structured
// blocks. The leading "$ " prompt is stripped from command lines.
func parseExampleBlocks(example string) []exampleBlock {
	var blocks []exampleBlock
	var cur exampleBlock
	flush := func() {
		if cur.comment != "" || len(cur.cmds) > 0 {
			blocks = append(blocks, cur)
			cur = exampleBlock{}
		}
	}
	for raw := range strings.SplitSeq(example, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "#"):
			// A comment after a command opens a new block.
			if len(cur.cmds) > 0 {
				flush()
			}
			c := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if cur.comment != "" {
				cur.comment += " " + c
			} else {
				cur.comment = c
			}
		default:
			cur.cmds = append(cur.cmds, strings.TrimSpace(strings.TrimPrefix(line, "$")))
		}
	}
	flush()
	return blocks
}

// writeExamples renders cmd.Example as a shell code block using Zensical/Material
// code annotations: each block's command carries a numeric (N) marker and its
// comment becomes the matching annotation list item below the block, instead of
// inline "# comment" lines. The theme enables content.code.annotate. Blocks with
// no comment render as a plain command line.
func writeExamples(b *bytes.Buffer, example string) {
	blocks := parseExampleBlocks(example)
	if len(blocks) == 0 {
		return
	}
	b.WriteString("## Examples\n\n```sh\n")
	var notes []string
	for _, blk := range blocks {
		marker := ""
		if blk.comment != "" && len(blk.cmds) > 0 {
			notes = append(notes, blk.comment)
			marker = fmt.Sprintf(" # (%d)!", len(notes))
		}
		for i, cmd := range blk.cmds {
			if i == len(blk.cmds)-1 {
				fmt.Fprintf(b, "%s%s\n", cmd, marker)
			} else {
				fmt.Fprintf(b, "%s\n", cmd)
			}
		}
	}
	b.WriteString("```\n\n")
	for i, note := range notes {
		fmt.Fprintf(b, "%d. %s\n", i+1, escapeText(note))
	}
	b.WriteString("\n")
}

// writeSubcommands renders a command's children as link lists. When the command
// declares cobra groups, children are split under one section per group (in
// declaration order) with an "Additional commands" section for the rest;
// otherwise a single "Subcommands" list is written. The sidebar carries the
// full tree, so there is no separate SEE ALSO section.
func writeSubcommands(b *bytes.Buffer, cmd *cobra.Command, linkHandler func(string) string) {
	subs := availableSubcommands(cmd)
	if len(subs) == 0 {
		return
	}

	groups := cmd.Groups()
	if len(groups) == 0 {
		writeSubcommandSection(b, "Subcommands", subs, linkHandler)
		return
	}

	known := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		known[g.ID] = struct{}{}
		var inGroup []*cobra.Command
		for _, c := range subs {
			if c.GroupID == g.ID {
				inGroup = append(inGroup, c)
			}
		}
		writeSubcommandSection(b, g.Title, inGroup, linkHandler)
	}

	var rest []*cobra.Command
	for _, c := range subs {
		if _, ok := known[c.GroupID]; c.GroupID == "" || !ok {
			rest = append(rest, c)
		}
	}
	writeSubcommandSection(b, "Additional commands", rest, linkHandler)
}

// writeSubcommandSection writes one titled list of subcommand links. Nothing is
// written for an empty list.
func writeSubcommandSection(b *bytes.Buffer, title string, subs []*cobra.Command, linkHandler func(string) string) {
	if len(subs) == 0 {
		return
	}
	fmt.Fprintf(b, "## %s\n\n", title)
	for _, c := range subs {
		link := linkHandler(childRelLink(c))
		fmt.Fprintf(b, "- [`%s`](%s): %s\n", c.CommandPath(), link, c.Short)
	}
	fmt.Fprintln(b)
}

// availableSubcommands returns cmd's visible subcommands sorted by name, for
// deterministic output.
func availableSubcommands(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() && !c.IsAdditionalHelpTopicCommand() {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Reference-nav markers delimit the machine-owned block in zensical.toml. Only
// the text between them is replaced by GenReferenceNav; the curated topic
// sections of the nav are left untouched.
const (
	ReferenceNavStartMarker = "# >>> gen-docs:reference-nav (generated — do not edit by hand)"
	ReferenceNavEndMarker   = "# <<< gen-docs:reference-nav"
)

// GenReferenceNav renders the zensical.toml nav element for the generated
// reference tree: a single `{ Reference = [ { "jira" = [...] } ] }` entry that
// mirrors the command tree. Paths come from docRelPath and children from
// availableSubcommands — the same helpers the page walker uses — so the nav can
// never disagree with the generated page filenames. indent is the leading
// whitespace for the element, matching the nav array's element indentation.
func GenReferenceNav(root *cobra.Command, indent string) string {
	const step = "    "
	var b strings.Builder
	b.WriteString(indent + "{ Reference = [\n")
	writeNavCommand(&b, root, indent+step)
	b.WriteString(indent + "] },\n")
	return b.String()
}

// writeNavCommand writes one command's nav entry: a leaf is a single
// `{ "name" = "path.md" }`, a parent is `{ "name" = [ "path/index.md", … ] }`
// with its children nested one step deeper.
func writeNavCommand(b *strings.Builder, cmd *cobra.Command, indent string) {
	const step = "    "
	path := "reference/" + docRelPath(cmd)
	subs := availableSubcommands(cmd)
	if len(subs) == 0 {
		fmt.Fprintf(b, "%s{ %q = %q },\n", indent, cmd.Name(), path)
		return
	}
	fmt.Fprintf(b, "%s{ %q = [\n", indent, cmd.Name())
	fmt.Fprintf(b, "%s%q,\n", indent+step, path)
	for _, c := range subs {
		writeNavCommand(b, c, indent+step)
	}
	fmt.Fprintf(b, "%s] },\n", indent)
}

// SpliceReferenceNav replaces the text between the reference-nav markers in
// config with navBlock, leaving everything else untouched. It errors if the
// markers are missing or out of order.
func SpliceReferenceNav(config, navBlock string) (string, error) {
	lines := strings.Split(config, "\n")
	start, end := -1, -1
	for i, ln := range lines {
		switch {
		case strings.Contains(ln, ReferenceNavStartMarker):
			start = i
		case strings.Contains(ln, ReferenceNavEndMarker):
			end = i
		}
	}
	if start == -1 || end == -1 || end <= start {
		return "", fmt.Errorf("docs: reference-nav markers not found (or out of order) in config")
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[:start+1]...)
	out = append(out, strings.Split(strings.TrimRight(navBlock, "\n"), "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}
