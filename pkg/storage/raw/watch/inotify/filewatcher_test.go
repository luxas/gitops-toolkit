package inotify

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rjeczalik/notify"
	"github.com/weaveworks/libgitops/pkg/storage/raw/watch"
	"golang.org/x/sys/unix"
)

type testEventWrapper struct {
	event notify.Event
}

func (t *testEventWrapper) Event() notify.Event {
	return t.event
}

func (t *testEventWrapper) Path() string     { return "" }
func (t *testEventWrapper) Sys() interface{} { return &unix.InotifyEvent{} }

var _ notify.EventInfo = &testEventWrapper{}

func testEvent(event notify.Event) notify.EventInfo {
	return &testEventWrapper{event}
}

var testEvents = []notifyEvents{
	{
		testEvent(notify.InDelete),
		testEvent(notify.InCloseWrite),
		testEvent(notify.InMovedTo),
	},
	{
		testEvent(notify.InCloseWrite),
		testEvent(notify.InDelete),
		testEvent(notify.InDelete),
	},
	{
		testEvent(notify.InCloseWrite),
		testEvent(notify.InMovedTo),
		testEvent(notify.InMovedFrom),
		testEvent(notify.InDelete),
	},
	{
		testEvent(notify.InDelete),
		testEvent(notify.InCloseWrite),
	},
	{
		testEvent(notify.InCloseWrite),
		testEvent(notify.InDelete),
	},
}

var targets = []FileEventTypes{
	{
		watch.FileEventModify,
	},
	{
		watch.FileEventDelete,
	},
	{
		watch.FileEventModify,
		watch.FileEventMove,
		watch.FileEventDelete,
	},
	{
		watch.FileEventModify,
	},
	{},
}

func extractEventTypes(events FileEvents) (eventTypes FileEventTypes) {
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
	}

	return
}

func eventsEqual(a, b FileEventTypes) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// FileEventTypes is a slice of FileEventType
type FileEventTypes []watch.FileEventType

var _ fmt.Stringer = FileEventTypes{}

func (e FileEventTypes) String() string {
	strs := make([]string, 0, len(e))
	for _, ev := range e {
		strs = append(strs, ev.String())
	}

	return strings.Join(strs, ",")
}

func TestEventConcatenation(t *testing.T) {
	for i, e := range testEvents {
		result := extractEventTypes((&FileWatcher{}).concatenateEvents(e))
		if !eventsEqual(result, targets[i]) {
			t.Errorf("wrong concatenation result: %v != %v", result, targets[i])
		}
	}
}