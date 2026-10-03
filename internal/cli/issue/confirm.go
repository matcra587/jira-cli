package issue

import (
	"errors"
	"fmt"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

// The ONE answer to "when does a write ask before acting, and what
// happens when it cannot".
//
// It was `issue comment delete`'s alone until `issue attachment delete`
// arrived, and the extraction is the point rather than tidiness: two
// destructive commands answering that question separately is how they
// come to answer it differently, and the difference nobody notices is
// the one where the second command deletes without asking.
//
// **There are now TWO reasons a write asks, not one**, and the gate is
// still single because the answer to "what happens when it cannot ask"
// is identical for both:
//
//  1. The act cannot be undone — the three deletes.
//  2. The caller did not NAME the targets. A bulk selector (`--jql`,
//     `--filter`, `--from-file`) picks the issues by query, so the
//     invocation itself does not say which ones, and `issue edit --jql`
//     can rewrite a hundred issues nobody listed. An edit is reversible
//     in principle and that is thin comfort at a hundred issues: the
//     caller would have to know which hundred to reverse, which is
//     exactly the fact the invocation withheld.
//
// Reason 2 is why this file's question type carries its own Risk clause
// rather than assuming deletion — the refusal a non-interactive caller
// reads has to describe the act it actually refused.
//
// What is SHARED is the policy — when to prompt, when to refuse, which
// code the refusal carries. What each command keeps is its own QUESTION,
// because a dialog that cannot name what it is about to destroy is not
// worth drawing.

// confirmFunc is the dialog seam, spelled once so both callers and the
// Deps field that carries it agree on the shape.
type confirmFunc func(prompt.Confirm) (bool, error)

// destructiveQuestion is one command's own wording for the shared gate.
type destructiveQuestion struct {
	// Subject names the thing at stake, for the refusal a
	// non-interactive caller gets — which has no dialog to read and
	// otherwise could not say what was not deleted.
	Subject string
	// Risk is the whole first clause of that refusal: what the act is and
	// why it needs confirming ("deleting KEY-1 cannot be undone, so it
	// needs confirmation").
	//
	// Carried rather than composed here because the two reasons a write
	// asks are different sentences (see this file's own doc comment), and
	// a gate that wrote the sentence itself could only write one of them —
	// which is how a selector-driven `issue edit` would come to refuse
	// with a message about deletion.
	Risk string
	// Question is the alternating plain/value parts prompt.Question
	// takes, so the values in the sentence are highlighted. Passed
	// through rather than pre-rendered because that function has to build
	// the whole string for its styling to nest correctly (see its own doc
	// comment).
	Question []string
	// Caveat qualifies the question in the quieter register: what will
	// still be true afterwards.
	Caveat string
	// Affirmative and Negative label the buttons. Name the act, never
	// "Yes" and "No" — the labels are read after the question and have to
	// make sense alone.
	Affirmative string
	Negative    string
}

// risk is Risk, or a general fallback for a question that did not set
// one. The fallback names the subject and claims nothing about
// reversibility, because a question that did not say why it is asking is
// not evidence that the act cannot be undone.
func (q destructiveQuestion) risk() string {
	if q.Risk != "" {
		return q.Risk
	}
	return "acting on " + q.Subject + " needs confirmation"
}

// errDestructiveAborted reports that the person said no. A sentinel
// rather than a coded error at the point of asking, so the command that
// knows what was abandoned writes the message.
var errDestructiveAborted = errors.New("destructive action aborted")

// confirmDestructive gates a destructive path.
//
// **Why there is a confirmation at all, on an agent-first CLI.** The
// honest case against one is that these dialogs cannot identify what
// they are about to destroy: they name an id the caller just typed, and
// showing the content would cost a read the command otherwise never
// makes. So this is not a "check this is the right one" gate, and no
// wording here claims to be. What it IS, and what earns its keystroke,
// is an interrupt in front of an action with no undo — Jira keeps no
// trash for comments or attachments. A mistyped id at a terminal is
// otherwise unrecoverable between pressing return and reading the
// output.
//
// **Why an agent is never prompted, and never silently allowed either.**
// Invocation.Interactive is false for a detected agent even when it
// holds a real PTY, so a dialog would hang a harness that passes every
// terminal check. Prompting is therefore off the table there. The other
// two options are to delete anyway or to refuse, and refusing is the one
// that keeps the destructive default from being "yes": an agent that
// meant it says so with --force, in the invocation, where a reviewer
// reading the transcript can see it. That is also what keeps these
// commands scriptable — --force is always available, terminal or not,
// and it is the ONLY thing a non-interactive caller has to add.
//
// A preview never reaches here: every caller returns before the gate on
// a dry run, because a preview destroys nothing and a dialog in front of
// it would be theater.
func confirmDestructive(ask confirmFunc, inv command.Invocation, force bool, q destructiveQuestion) error {
	if force {
		return nil
	}
	if !inv.Interactive {
		// Two ways to arrive here, and they need different sentences. The
		// caller who passed --no-input told us not to ask and is owed an
		// answer in those terms; everyone else got here because nothing
		// could be asked, which is the case the original wording covers.
		reason := "there is no terminal here to ask at"
		if inv.NoInput {
			reason = "--" + cli.FlagNoInput + " means it cannot be asked for"
		}
		return refuseWithoutConfirmation(fmt.Sprintf(
			"%s — %s, so pass --%s to confirm up front",
			q.risk(), reason, command.FlagForce,
		))
	}
	if ask == nil {
		// Unreachable from the composition root, which wires the real
		// dialog. A command that could reach here would delete without
		// asking, so it refuses instead: the safe answer to "I was built
		// without the thing that gates this" is not to proceed.
		return refuseWithoutConfirmation(fmt.Sprintf(
			"no confirmation dialog is available, so acting on %s cannot be confirmed — pass --%s to confirm up front",
			q.Subject, command.FlagForce,
		))
	}

	ok, err := ask(prompt.Confirm{
		Question:    prompt.Question(q.Question...),
		Caveat:      q.Caveat,
		Affirmative: q.Affirmative,
		Negative:    q.Negative,
	})
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			// Closing the form is a choice, not a failure of the dialog —
			// the same reading confirmLogout gives it (auth/form.go).
			return errDestructiveAborted
		}
		return err
	}
	if !ok {
		return errDestructiveAborted
	}
	return nil
}

// selectedWrite is one command's wording for the UNNAMED-TARGET gate:
// the act, spelled the two ways the sentences need it.
type selectedWrite struct {
	// Gerund is the act in prose, for the refusal ("editing").
	Gerund string
	// Imperative names the act on the confirm button ("Edit"). Never
	// "Yes" — the labels are read after the question and have to make
	// sense alone (destructiveQuestion's own rule).
	Imperative string
}

// confirmSelected gates a write whose targets came from a SELECTOR, and
// is a no-op for one whose caller typed the keys.
//
// **This is what a bulk selector costs**, and it is deliberately the only
// thing it costs. `issue edit KEY-1 KEY-2` prompts for nothing, exactly
// as before; `issue edit --jql …` prompts, because the invocation does
// not say which issues it means. The gate is on the AMBIGUITY, not on the
// verb — which is why four commands that never had a confirmation now
// have one, and why they still do not have one when keys are named.
//
// It is reached only after the selector has RESOLVED (design §3.5 rule
// 1): t.Keys is the actual list, so the question names the issues rather
// than echoing the query back. Echoing the query would ask the caller to
// re-verify the thing they already got wrong, and the count is the part
// they cannot derive by reading it.
//
// --force skips this prompt as it skips every other, and skipping it does
// NOT skip the enumeration: the keys are already resolved by the time
// this is called, and they reach the envelope on every path.
func confirmSelected(ask confirmFunc, inv command.Invocation, force bool, act selectedWrite, t command.Targets) error {
	if t.Summary == nil {
		return nil
	}
	subject := joinPlainKeys(t.Keys)
	return confirmDestructive(ask, inv, force, destructiveQuestion{
		Subject: subject,
		Risk: fmt.Sprintf(
			"%s %s, which --%s chose rather than you naming them, needs confirmation",
			act.Gerund, subject, t.Summary.Kind,
		),
		Question: []string{act.Imperative + " ", subject, "?"},
		Caveat: fmt.Sprintf(
			"--%s matched these %s just now; a different set may match later.",
			t.Summary.Kind, plainIssueCount(len(t.Keys)),
		),
		Affirmative: act.Imperative,
		Negative:    "Cancel",
	})
}

// plainIssueCount renders a target count for one clause of a caveat.
func plainIssueCount(n int) string {
	if n == 1 {
		return "issue"
	}
	return fmt.Sprintf("%d issues", n)
}

// refuseWithoutConfirmation builds the refusal both non-interactive
// paths give.
//
// CodeFlagRequired: something the caller must add is missing, and its
// own hint plus this message name exactly what. Field points at --force
// so an agent recovering from this reads the fix off a field rather than
// out of prose.
func refuseWithoutConfirmation(message string) error {
	e := cli.NewInputError(cli.CodeFlagRequired, message)
	e.Field = command.FlagForce
	return e
}
