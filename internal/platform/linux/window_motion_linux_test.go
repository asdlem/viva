package linux

import (
	"testing"
	"unsafe"
)

func TestQueuedDragMotionKeepsLatestPositionBeforeRelease(t *testing.T) {
	events := make([]xcbInputEvent, 101)
	for index := range 100 {
		events[index] = xcbInputEvent{ResponseType: xcbMotionNotify, Event: 1, State: xcbButton1Mask, EventX: int16(index)}
	}
	events[100] = xcbInputEvent{ResponseType: xcbButtonRelease, Event: 1, State: xcbButton1Mask}
	read, freed := 0, 0
	state := linuxWindowState{win: &window{api: &xcbAPI{
		pollForEvent: func(uintptr) unsafe.Pointer {
			if read == len(events) {
				return nil
			}
			event := &events[read]
			read++
			return unsafe.Pointer(event)
		},
		free: func(unsafe.Pointer) { freed++ },
	}}}
	motion := (*xcbInputEvent)(state.nextEvent())
	if motion == nil || motion.ResponseType != xcbMotionNotify || motion.EventX != 99 {
		t.Fatalf("drag delivered stale position: %+v", motion)
	}
	if release := state.nextEvent(); release != unsafe.Pointer(&events[100]) {
		t.Fatal("drag compression lost or reordered the button release")
	}
	if freed != 99 || state.nextEvent() != nil {
		t.Fatalf("discarded motion count = %d, want 99, with an empty queue", freed)
	}
}

func TestQueuedDragMotionPreservesInputBoundaries(t *testing.T) {
	for _, boundary := range []xcbInputEvent{
		{ResponseType: xcbMotionNotify, Event: 2, State: xcbButton1Mask},
		{ResponseType: xcbMotionNotify, Event: 1, Root: 2, State: xcbButton1Mask},
		{ResponseType: xcbMotionNotify, Event: 1, Child: 2, State: xcbButton1Mask},
		{ResponseType: xcbMotionNotify, Event: 1, SameScreen: 1, State: xcbButton1Mask},
		{ResponseType: xcbMotionNotify, Event: 1, State: xcbButton1Mask | xcbModShift},
		{ResponseType: xcbKeyPress},
		{ResponseType: xcbLeaveNotify},
	} {
		first := xcbInputEvent{ResponseType: xcbMotionNotify, Event: 1, State: xcbButton1Mask}
		queue := []unsafe.Pointer{unsafe.Pointer(&first), unsafe.Pointer(&boundary)}
		state := linuxWindowState{win: &window{api: &xcbAPI{
			pollForEvent: func(uintptr) unsafe.Pointer {
				if len(queue) == 0 {
					return nil
				}
				event := queue[0]
				queue = queue[1:]
				return event
			},
			free: func(unsafe.Pointer) { t.Fatal("an input boundary was discarded") },
		}}}
		if state.nextEvent() != unsafe.Pointer(&first) || state.nextEvent() != unsafe.Pointer(&boundary) {
			t.Fatalf("input boundary reordered: %+v", boundary)
		}
	}
}

func TestQueuedHoverMotionIsNotCompressed(t *testing.T) {
	events := []xcbInputEvent{{ResponseType: xcbMotionNotify}, {ResponseType: xcbMotionNotify}}
	read := 0
	state := linuxWindowState{win: &window{api: &xcbAPI{
		pollForEvent: func(uintptr) unsafe.Pointer {
			event := &events[read]
			read++
			return unsafe.Pointer(event)
		},
		free: func(unsafe.Pointer) { t.Fatal("hover motion was discarded") },
	}}}
	if state.nextEvent() != unsafe.Pointer(&events[0]) || read != 1 {
		t.Fatal("hover motion read past the current event")
	}
}

func TestQueuedDragMotionBoundsTheBatch(t *testing.T) {
	events := make([]xcbInputEvent, 300)
	read := 0
	state := linuxWindowState{win: &window{api: &xcbAPI{
		pollForEvent: func(uintptr) unsafe.Pointer {
			if read == len(events) {
				return nil
			}
			event := &events[read]
			*event = xcbInputEvent{ResponseType: xcbMotionNotify, State: xcbButton1Mask, EventX: int16(read)}
			read++
			return unsafe.Pointer(event)
		},
		free: func(unsafe.Pointer) {},
	}}}
	if event := (*xcbInputEvent)(state.nextEvent()); event.EventX != 256 || read != 257 {
		t.Fatalf("motion batch did not yield: position %d, read %d events", event.EventX, read)
	}
	if event := (*xcbInputEvent)(state.nextEvent()); event.EventX != 299 {
		t.Fatalf("remaining motion was lost: position %d", event.EventX)
	}
}

func BenchmarkQueuedDragMotion(benchmark *testing.B) {
	for _, compress := range []bool{false, true} {
		name := "uncompressed"
		if compress {
			name = "compressed"
		}
		benchmark.Run(name, func(benchmark *testing.B) {
			var events [100]xcbInputEvent
			for index := range events {
				events[index] = xcbInputEvent{ResponseType: xcbMotionNotify, Event: 1, State: xcbButton1Mask, EventX: int16(index)}
			}
			read := 0
			state := linuxWindowState{win: &window{api: &xcbAPI{
				pollForEvent: func(uintptr) unsafe.Pointer {
					if read == len(events) {
						return nil
					}
					event := &events[read]
					read++
					return unsafe.Pointer(event)
				},
				free: func(unsafe.Pointer) {},
			}}}
			delivered := 0
			benchmark.ReportAllocs()
			for benchmark.Loop() {
				read = 0
				for {
					var event unsafe.Pointer
					if compress {
						event = state.nextEvent()
					} else {
						event = state.win.api.pollForEvent(0)
					}
					if event == nil {
						break
					}
					delivered++
				}
			}
			benchmark.ReportMetric(float64(delivered)/float64(benchmark.N), "deliveries/burst")
		})
	}
}
