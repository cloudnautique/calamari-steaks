package bridge

import (
	"encoding/binary"
	"errors"
)

// rfbFilter splits the client-to-server RFB byte stream into whole messages so
// the server can enforce input ownership. Handshake bytes and viewer protocol
// messages (pixel format, encodings, update requests, fences) always pass;
// keyboard, pointer, clipboard and desktop-size messages require the view to
// hold the box's input claim. Unknown handshakes or messages fail closed.
//
// Supported handshakes: RFB 3.7/3.8 with security type None (1) or VNC
// authentication (2), which covers the Discobox x11vnc/websockify desktop.
type rfbFilter struct {
	phase   rfbPhase
	pending []byte
}

type rfbPhase int

const (
	rfbVersion rfbPhase = iota
	rfbSecurity
	rfbVNCAuth
	rfbClientInit
	rfbMessages
)

// rfbClipboardCaps is the extended clipboard caps action flag. A message
// carrying it announces capabilities; its other action bits list what the
// client supports rather than asking for anything.
const rfbClipboardCaps = 1 << 24

// rfbPending bounds a partially received message (mostly large clipboard text).
const rfbPending = 1 << 20

type rfbSegment struct {
	data  []byte
	input bool
}

var errRFBUnsupported = errors.New("unsupported desktop protocol message; reopen the desktop")

// feed consumes a batch and returns every message completed by it, in order.
// Bytes of an incomplete trailing message are retained for the next batch.
func (f *rfbFilter) feed(batch []byte) ([]rfbSegment, error) {
	f.pending = append(f.pending, batch...)
	var segments []rfbSegment
	for len(f.pending) > 0 {
		size, input, err := f.next(f.pending)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			if len(f.pending) > rfbPending {
				return nil, errors.New("desktop message exceeds its bound")
			}
			break
		}
		segments = append(segments, rfbSegment{data: append([]byte(nil), f.pending[:size]...), input: input})
		f.pending = f.pending[size:]
	}
	if len(f.pending) == 0 {
		f.pending = nil
	}
	return segments, nil
}

// next returns the length of the first complete message in data, or 0 when
// more bytes are needed, and advances the handshake phase when one completes.
func (f *rfbFilter) next(data []byte) (int, bool, error) {
	switch f.phase {
	case rfbVersion:
		if len(data) < 12 {
			return 0, false, nil
		}
		if version := string(data[:12]); version != "RFB 003.008\n" && version != "RFB 003.007\n" {
			return 0, false, errRFBUnsupported
		}
		f.phase = rfbSecurity
		return 12, false, nil
	case rfbSecurity:
		switch data[0] {
		case 1:
			f.phase = rfbClientInit
		case 2:
			f.phase = rfbVNCAuth
		default:
			return 0, false, errRFBUnsupported
		}
		return 1, false, nil
	case rfbVNCAuth:
		if len(data) < 16 {
			return 0, false, nil
		}
		f.phase = rfbClientInit
		return 16, false, nil
	case rfbClientInit:
		f.phase = rfbMessages
		return 1, false, nil
	}
	need := func(n int) int {
		if len(data) < n {
			return 0
		}
		return n
	}
	switch data[0] {
	case 0: // SetPixelFormat
		return need(20), false, nil
	case 2: // SetEncodings
		if len(data) < 4 {
			return 0, false, nil
		}
		return need(4 + 4*int(binary.BigEndian.Uint16(data[2:4]))), false, nil
	case 3: // FramebufferUpdateRequest
		return need(10), false, nil
	case 150: // EnableContinuousUpdates
		return need(10), false, nil
	case 248: // ClientFence
		if len(data) < 9 {
			return 0, false, nil
		}
		return need(9 + int(data[8])), false, nil
	case 4: // KeyEvent
		return need(8), true, nil
	case 5: // PointerEvent; the marker bit selects the 7-byte extended form.
		if len(data) < 2 {
			return 0, true, nil
		}
		if data[1]&0x80 != 0 {
			return need(7), true, nil
		}
		return need(6), true, nil
	case 6: // ClientCutText; a negative length is the extended clipboard format.
		if len(data) < 8 {
			return 0, true, nil
		}
		length := int64(int32(binary.BigEndian.Uint32(data[4:8])))
		extended := length < 0
		if extended {
			length = -length
		}
		if length > rfbPending-8 {
			return 0, true, errors.New("desktop clipboard exceeds its bound")
		}
		// An extended clipboard capabilities announcement is negotiation, not
		// input: noVNC sends it on connect, while every view is still a viewer,
		// with the caps flag plus every action it supports. Dropping it would
		// leave the view that later takes control without clipboard.
		if extended && length >= 4 {
			if len(data) < 12 {
				return 0, true, nil
			}
			caps := binary.BigEndian.Uint32(data[8:12])&rfbClipboardCaps != 0
			return need(8 + int(length)), !caps, nil
		}
		return need(8 + int(length)), true, nil
	case 250: // XVP (shutdown/reboot/reset)
		return need(4), true, nil
	case 251: // SetDesktopSize
		if len(data) < 8 {
			return 0, true, nil
		}
		return need(8 + 16*int(data[6])), true, nil
	case 255: // QEMU extended key event is the only QEMU message noVNC sends.
		if len(data) < 2 {
			return 0, true, nil
		}
		if data[1] != 0 {
			return 0, true, errRFBUnsupported
		}
		return need(12), true, nil
	}
	return 0, false, errRFBUnsupported
}
