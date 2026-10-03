package jira

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

type commentFailureReader struct{ cause error }

func (r commentFailureReader) Read([]byte) (int, error) { return 0, r.cause }

func TestCommentReceiptPreservesTrailingReadFailure(t *testing.T) {
	t.Parallel()
	cause := errors.New("fixture receipt stream failed")
	comment, err := decodeCommentReceipt(io.MultiReader(strings.NewReader(`{"id":"10042"}`), commentFailureReader{cause: cause}), "10042")
	if comment != nil || !errors.Is(err, cause) || !errors.Is(err, core.ErrCommentResponseInvalid) {
		t.Fatal("comment receipt hid the original read failure or published an incomplete response")
	}
}

func TestCommentReceiptPreservesNativeBodyBytes(t *testing.T) {
	t.Parallel()
	body := `{"version":1,"type":"doc","future":900719925474099312345,"content":[{"type":"paragraph","content":[{"type":"text","text":"<&>\u2028"}]}]}`
	comment, err := decodeCommentReceipt(strings.NewReader(`{"id":"10042","body":`+body+`,"future":"kept"} `), "10042")
	if err != nil || comment == nil || !comment.Body.Set || comment.Body.Null || !bytes.Equal(comment.Body.Value, []byte(body)) {
		t.Fatal("comment receipt changed native ADF bytes or refused valid trailing whitespace")
	}
}

func TestCommentPagePreservesOriginalReadFailures(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"", `{"startAt":0,"maxResults":50,"total":0,"comments":[]}`} {
		t.Run(map[bool]string{false: "initial", true: "trailing"}[prefix != ""], func(t *testing.T) {
			t.Parallel()
			cause := errors.New("fixture comment page stream failed")
			page, err := decodeCommentPage(io.MultiReader(strings.NewReader(prefix), commentFailureReader{cause: cause}), 0, 50)
			if page != nil || !errors.Is(err, cause) || !errors.Is(err, core.ErrCommentResponseInvalid) {
				t.Fatal("comment page hid the original reader failure or published an incomplete document")
			}
		})
	}
}

func TestCommentPagePreservesLargeNativeBodyBytes(t *testing.T) {
	t.Parallel()
	body := `{"version":1,"type":"doc","future":900719925474099312345,"content":[{"type":"paragraph","content":[{"type":"text","text":"<&>\u2028` + strings.Repeat("body", 25000) + `"}]}]}`
	page, err := decodeCommentPage(strings.NewReader(`{"startAt":0,"maxResults":50,"total":1,"comments":[{"id":"10042","body":`+body+`}]} `), 0, 50)
	if err != nil || page == nil || len(page.Comments) != 1 || !bytes.Equal(page.Comments[0].Body.Value, []byte(body)) {
		t.Fatal("comment page changed native ADF bytes or applied an invented small-body limit")
	}
}
