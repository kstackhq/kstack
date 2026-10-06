package graph

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/securityconfig"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// TestChatRefusalsCarryTheirCode lists one case per entry of chatRefusals by hand,
// since its package cannot see the table. A new entry fails here until it has a case
// there.
func TestTheChatRefusalTableIsPinned(t *testing.T) {
	if got := len(chatRefusals); got != 9 {
		t.Fatalf("chatRefusals has %d entries; give TestChatRefusalsCarryTheirCode a case for each, then pin the count", got)
	}
}

// mapStream applies mapFn to every source value in order, then closes the
// output and runs unsub when the source channel closes.
func TestMapStreamMapsValuesAndClosesWithSource(t *testing.T) {
	sub := make(chan int)
	unsubbed := make(chan struct{})

	out := mapStream(context.Background(), sub, func() { close(unsubbed) }, strconv.Itoa)

	go func() {
		sub <- 1
		sub <- 2
		close(sub)
	}()

	for _, want := range []string{"1", "2"} {
		if got := testutil.Recv(t, out, "a mapped value"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}

	testutil.RecvClosed(t, out, "the output channel after the source closed")
	testutil.RecvClosed(t, unsubbed, "the unsub channel")
}

// Cancelling ctx tears the stream down: the output channel closes and unsub
// runs, even though the source channel stays open.
func TestMapStreamStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sub := make(chan int)
	unsubbed := make(chan struct{})

	out := mapStream(ctx, sub, func() { close(unsubbed) }, strconv.Itoa)

	cancel()

	testutil.RecvClosed(t, out, "the output channel after ctx cancel")
	testutil.RecvClosed(t, unsubbed, "the unsub channel")
}

// Cancelling ctx while the pump is blocked sending a mapped value (no reader
// on the output channel) still tears the stream down — the send must not wedge
// the goroutine past cancellation.
func TestMapStreamStopsOnContextCancelDuringSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sub := make(chan int)
	unsubbed := make(chan struct{})

	out := mapStream(ctx, sub, func() { close(unsubbed) }, strconv.Itoa)

	// The source is unbuffered, so this returns only once the pump has taken the
	// value — which leaves it blocked sending "1" to the unread output.
	sub <- 1
	cancel()

	// Wait on unsub before reading out: a reader would make the blocked send
	// ready beside ctx.Done, and select would pick between them at random.
	testutil.RecvClosed(t, unsubbed, "the unsub channel")
	testutil.RecvClosed(t, out, "the output channel")
}

// A grant the settings refuse by its shape is a validation error under the
// folder rule, carrying the settings' own reason.
func TestFolderErrNamesARefusedShape(t *testing.T) {
	refusal := securityconfig.Refusal{Field: "rules", Value: "x", Reason: "is not a rule"}
	var e *gqlerror.Error
	if !errors.As(folderErr(refusal), &e) {
		t.Fatalf("folderErr(%v) is not a GraphQL error", refusal)
	}
	if e.Extensions["code"] != "KSTACK_VALIDATION_ERROR" || e.Extensions["rule"] != "folder" || e.Message != refusal.Error() {
		t.Errorf("folderErr(%v) = %q %v", refusal, e.Message, e.Extensions)
	}
}
