package bridge

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// RFB client message types this filter frames (RFC 6143 and the community
// extensions noVNC uses).
const (
	rfbSetPixelFormat           = 0
	rfbSetEncodings             = 2
	rfbFramebufferUpdateRequest = 3
	rfbKeyEvent                 = 4
	rfbPointerEvent             = 5
	rfbClientCutText            = 6
	rfbEnableContinuousUpdates  = 150
	rfbClientFence              = 248
	rfbXvp                      = 250
	rfbSetDesktopSize           = 251
	rfbQEMU                     = 255
)

// pseudoEncodingExtendedMouseButtons, once a server confirms it, makes noVNC
// send 7-byte PointerEvents, which cannot be told apart from 6-byte ones in
// the client stream alone. The filter withholds it from SetEncodings so the
// client stream stays framed without reading the server's.
const pseudoEncodingExtendedMouseButtons = -316

// The extended clipboard caps action flag; a message carrying it is a
// capabilities announcement, which is not input.
const rfbClipboardCaps = 1 << 24

// Bounds on what one message may claim, so a malformed length cannot make the
// filter forward or discard an unbounded stream as a single message.
const (
	rfbMaxCutText  = 16 << 20
	rfbMaxEncoding = 1024
)

type rfbPhase int

const (
	rfbVersion rfbPhase = iota
	rfbSecurityType
	rfbVNCAuth
	rfbClientInit
	rfbMessages
)

// rfbInputFilter follows the client-to-server half of an RFB connection and
// drops input messages (keys, pointer, clipboard, desktop resize, power
// operations) while the view does not hold input control, so the input lease
// is enforced by the bridge rather than only by the widget's view-only mode.
// Non-input messages always pass: a viewer must keep requesting framebuffer
// updates. It supports RFB 3.7 and later with security None or VNC
// authentication, which covers x11vnc -nopw; anything it cannot frame is an error, and the
// caller closes the desktop rather than guess at message boundaries.
type rfbInputFilter struct {
	phase  rfbPhase
	header []byte // the current message, until its length is known
	// remaining bytes of the current message after its header, and whether
	// they are dropped.
	remaining uint64
	drop      bool
}

// filter consumes one batch of client bytes and returns those to forward.
// owned is whether this view holds input control for the whole batch.
func (f *rfbInputFilter) filter(data []byte, owned bool) ([]byte, error) {
	out := make([]byte, 0, len(data))
	for len(data) > 0 {
		if f.remaining > 0 {
			n := uint64(len(data))
			if n > f.remaining {
				n = f.remaining
			}
			if !f.drop {
				out = append(out, data[:n]...)
			}
			data, f.remaining = data[n:], f.remaining-n
			continue
		}
		f.header = append(f.header, data[0])
		data = data[1:]
		emit, done, err := f.advance(owned)
		if err != nil {
			return nil, err
		}
		if done {
			out = append(out, emit...)
			f.header = f.header[:0]
		}
	}
	return out, nil
}

// advance inspects the bytes gathered so far. done reports that f.header is
// complete: emit is what to forward of it, and f.remaining/f.drop describe the
// body that follows.
func (f *rfbInputFilter) advance(owned bool) (emit []byte, done bool, err error) {
	h := f.header
	switch f.phase {
	case rfbVersion:
		if len(h) < 12 {
			return nil, false, nil
		}
		var major, minor int
		if _, err := fmt.Sscanf(string(h), "RFB %03d.%03d\n", &major, &minor); err != nil || major != 3 {
			return nil, false, errors.New("desktop sent an unsupported RFB protocol version")
		}
		// On 3.3 the server chooses the security type, so whether a 16-byte
		// VNC authentication response follows is not in the client stream.
		// Guessing wrong would shift every later message boundary.
		if minor < 7 {
			return nil, false, errors.New("desktop RFB 3.3 cannot be filtered; the desktop server must offer RFB 3.7 or later")
		}
		f.phase = rfbSecurityType
		return h, true, nil
	case rfbSecurityType:
		switch h[0] {
		case 1: // None
			f.phase = rfbClientInit
		case 2: // VNC authentication: a 16-byte response follows.
			f.phase, f.remaining, f.drop = rfbClientInit, 16, false
		default:
			return nil, false, fmt.Errorf("desktop security type %d cannot be filtered", h[0])
		}
		return h, true, nil
	case rfbClientInit:
		f.phase = rfbMessages
		return h, true, nil
	}
	input := false
	var need, body uint64
	switch h[0] {
	case rfbSetPixelFormat:
		need = 20
	case rfbFramebufferUpdateRequest, rfbEnableContinuousUpdates:
		need = 10
	case rfbKeyEvent:
		need, input = 8, true
	case rfbPointerEvent:
		need, input = 6, true
	case rfbXvp:
		need, input = 4, true
	case rfbSetEncodings:
		if len(h) < 4 {
			return nil, false, nil
		}
		count := uint64(binary.BigEndian.Uint16(h[2:4]))
		if count > rfbMaxEncoding {
			return nil, false, errors.New("desktop SetEncodings is too large")
		}
		need = 4 + 4*count
		if uint64(len(h)) < need {
			return nil, false, nil
		}
		return withoutExtendedMouseButtons(h), true, nil
	case rfbClientCutText:
		if len(h) < 8 {
			return nil, false, nil
		}
		// Extended clipboard sends the length negated.
		length := int64(int32(binary.BigEndian.Uint32(h[4:8])))
		extended := length < 0
		if extended {
			length = -length
		}
		if length > rfbMaxCutText {
			return nil, false, errors.New("desktop clipboard message is too large")
		}
		need, body, input = 8, uint64(length), true
		// An extended clipboard capabilities announcement is negotiation, not
		// input: noVNC sends it on connect, while every view is still a viewer.
		if extended && length >= 4 {
			if len(h) < 12 {
				return nil, false, nil
			}
			need, body = 12, uint64(length-4)
			// With the caps bit set, the other action bits list what the
			// client supports; they do not ask for anything.
			input = binary.BigEndian.Uint32(h[8:12])&rfbClipboardCaps == 0
		}
	case rfbClientFence:
		if len(h) < 9 {
			return nil, false, nil
		}
		need, body = 9, uint64(h[8])
	case rfbSetDesktopSize:
		if len(h) < 8 {
			return nil, false, nil
		}
		need, body, input = 8, 16*uint64(h[6]), true
	case rfbQEMU:
		if len(h) < 2 {
			return nil, false, nil
		}
		if h[1] != 0 { // only the extended key event is framed
			return nil, false, fmt.Errorf("desktop sent unsupported QEMU message %d", h[1])
		}
		need, input = 12, true
	default:
		return nil, false, fmt.Errorf("desktop sent unsupported RFB client message %d", h[0])
	}
	if uint64(len(h)) < need {
		return nil, false, nil
	}
	f.remaining, f.drop = body, input && !owned
	if f.drop {
		return nil, true, nil
	}
	return h, true, nil
}

// withoutExtendedMouseButtons rewrites a complete SetEncodings message.
func withoutExtendedMouseButtons(message []byte) []byte {
	out := append([]byte(nil), message[:4]...)
	count := 0
	for i := 4; i+4 <= len(message); i += 4 {
		if int32(binary.BigEndian.Uint32(message[i:i+4])) == pseudoEncodingExtendedMouseButtons {
			continue
		}
		out = append(out, message[i:i+4]...)
		count++
	}
	binary.BigEndian.PutUint16(out[2:4], uint16(count))
	return out
}
