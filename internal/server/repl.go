package server

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
)

// discardBufferedCRLF drops '\r' and '\n' bytes while they are already inside bufio's internal
// buffer (not yet delivered from the underlying Read). This must never block: we only inspect
// buffered data, never Peek when Buffered()==0.
func discardBufferedCRLF(br *bufio.Reader) {
	for br.Buffered() > 0 {
		peek, err := br.Peek(1)
		if err != nil || (peek[0] != '\r' && peek[0] != '\n') {
			return
		}
		_, _ = br.Discard(1)
	}
}

// readLineWithEcho reads a single line from an interactive SSH session (typically with a PTY).
//
// OpenSSH clients with a TTY send local keystrokes to the server without local echo; the remote
// shell normally echoes via kernel TTY rules. Because ClawSSH implements the REPL in Go, we must
// echo printable input ourselves. Line endings are normalized: CR, LF, or CRLF all terminate the
// line, matching common PTY behavior where Enter sends '\r' alone.
func readLineWithEcho(br *bufio.Reader, out io.Writer, log *slog.Logger) (string, error) {
	// A lone LF may arrive in the next TCP chunk after CR; it would otherwise be mistaken for an
	// empty line on the next read. Drop only bytes already sitting in bufio (never block).
	discardBufferedCRLF(br)

	var line []byte
	// runeStarts stores the start byte offset of each appended UTF-8 rune.
	// For ASCII this is equivalent to per-byte indexing; for multibyte runes
	// it lets Backspace remove one whole rune instead of one byte.
	var runeStarts []int
	for {
		c, err := br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) == 0 {
				return "", io.EOF
			}
			if errors.Is(err, io.EOF) {
				return string(line), nil
			}
			return "", err
		}

		switch c {
		case '\n':
			// If CR arrived in an earlier packet and LF in the next, we can see '\n' then immediate
			// command bytes. Treat that LF as stale rather than an empty line.
			if len(line) == 0 && br.Buffered() > 0 {
				peek, perr := br.Peek(1)
				if perr == nil && len(peek) > 0 && peek[0] != '\r' && peek[0] != '\n' {
					continue
				}
			}
			discardBufferedCRLF(br)
			return string(line), nil
		case '\r':
			// PTY often sends CR then LF. Never call Peek when nothing is buffered: Peek would
			// block on the next keystroke and merge two logical lines (double Enter / stray prompt).
			if br.Buffered() > 0 {
				peek, perr := br.Peek(1)
				if perr == nil && len(peek) > 0 && peek[0] == '\n' {
					if _, derr := br.Discard(1); derr != nil {
						log.Debug("discard crlf lf", "err", derr)
					}
				}
			}
			discardBufferedCRLF(br)
			return string(line), nil
		case 0x03: // Ctrl+C
			if _, werr := out.Write([]byte("^C\r\n")); werr != nil {
				log.Error("write ctrl+c", "err", werr)
			}
			return "", errInterrupt
		case 0x04: // Ctrl+D on empty line → EOF
			if len(line) == 0 {
				if _, werr := out.Write([]byte("\r\n")); werr != nil {
					log.Error("write ctrl+d newline", "err", werr)
				}
				return "", io.EOF
			}
			// Ignore Ctrl+D when the line is non-empty (matches common shell behavior).
			continue
		case 0x7f, 0x08: // DEL / BS
			if len(runeStarts) == 0 {
				continue
			}
			last := runeStarts[len(runeStarts)-1]
			runeStarts = runeStarts[:len(runeStarts)-1]
			line = line[:last]
			if _, werr := out.Write([]byte{'\b', ' ', '\b'}); werr != nil {
				return "", werr
			}
		default:
			// Skip control bytes, keep all printable UTF-8 bytes so CJK input works.
			if c < 0x20 {
				continue
			}
			// UTF-8 continuation bytes (10xxxxxx) belong to the current rune.
			if (c & 0xC0) != 0x80 {
				runeStarts = append(runeStarts, len(line))
			}
			line = append(line, c)
			if _, werr := out.Write([]byte{c}); werr != nil {
				return "", werr
			}
		}
	}
}

var errInterrupt = errors.New("user interrupt (ctrl+c)")
