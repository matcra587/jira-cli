package startup

import "strings"

// Globals holds the root persistent-flag values recovered from raw argv during
// the pre-cobra preflight: the requested config path and profile.
type Globals struct {
	ConfigPath string
	Profile    string
}

// GlobalsFromArgs scans argv for the --config/--profile (and -c/-P) global
// flags and returns their values, ignoring everything else.
func GlobalsFromArgs(args []string) Globals {
	return scanGlobals(args)
}

// SplitFirstCommandArg returns the argv prefix preceding the first
// non-flag token, that token (the command name), and the remaining args.
// ok is false when no command token is present.
func SplitFirstCommandArg(args []string) (prefix []string, command string, rest []string, ok bool) {
	_, prefix, command, rest, ok = parseArgs(args)
	return prefix, command, rest, ok
}

func parseArgs(args []string) (globals Globals, prefix []string, command string, rest []string, ok bool) {
	globals = scanGlobals(args)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 >= len(args) {
				return globals, args, "", nil, false
			}
			return globals, args[:i+1], args[i+1], args[i+2:], true
		}
		if consumeGlobal(args, &i, &globals) {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return globals, args[:i], arg, args[i+1:], true
	}
	return globals, args, "", nil, false
}

func scanGlobals(args []string) Globals {
	var globals Globals
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if after, ok := strings.CutPrefix(arg, "--"); ok {
			name, value, hasValue := strings.Cut(after, "=")
			switch name {
			case "config":
				globals.ConfigPath = flagValue(args, &i, value, hasValue)
			case "profile":
				globals.Profile = flagValue(args, &i, value, hasValue)
			}
			continue
		}
		if strings.HasPrefix(arg, "-c") && arg != "-" {
			globals.ConfigPath = shortFlagValue(args, &i, arg)
			continue
		}
		if strings.HasPrefix(arg, "-P") && arg != "-" {
			globals.Profile = shortFlagValue(args, &i, arg)
			continue
		}
	}
	return globals
}

func consumeGlobal(args []string, i *int, globals *Globals) bool {
	arg := args[*i]
	if after, ok := strings.CutPrefix(arg, "--"); ok {
		name, value, hasValue := strings.Cut(after, "=")
		switch name {
		case "config":
			globals.ConfigPath = flagValue(args, i, value, hasValue)
			return true
		case "profile":
			globals.Profile = flagValue(args, i, value, hasValue)
			return true
		case "output", "timeout", "color":
			_ = flagValue(args, i, value, hasValue)
			return true
		case "interactive", "debug", "no-input", "adf-strict", "adf-best-effort":
			return true
		default:
			return hasValue
		}
	}
	if strings.HasPrefix(arg, "-c") && arg != "-" {
		globals.ConfigPath = shortFlagValue(args, i, arg)
		return true
	}
	if strings.HasPrefix(arg, "-P") && arg != "-" {
		globals.Profile = shortFlagValue(args, i, arg)
		return true
	}
	// -o is --output's shorthand; the preflight ignores the mode but must
	// still consume its value so the spaced form (-o json) does not leave the
	// value to be mistaken for the command token.
	if strings.HasPrefix(arg, "-o") && arg != "-" {
		_ = shortFlagValue(args, i, arg)
		return true
	}
	return arg == "-i" || arg == "-d"
}

func flagValue(args []string, i *int, value string, hasValue bool) string {
	if hasValue {
		return value
	}
	if *i+1 >= len(args) {
		return ""
	}
	*i = *i + 1
	return args[*i]
}

func shortFlagValue(args []string, i *int, arg string) string {
	if len(arg) > 2 {
		value := strings.TrimPrefix(arg[2:], "=")
		return value
	}
	if *i+1 >= len(args) {
		return ""
	}
	*i = *i + 1
	return args[*i]
}
