package setup

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// errCancelled is the person at the terminal backing out of a question.
var errCancelled = errors.New("cancelled")

// prompter asks a first install's questions on the terminal. Each
// question is erased once answered: the answers are shown as steps.
type prompter struct {
	tty   *os.File
	in    *bufio.Reader
	paint paint
}

func newPrompter(tty *os.File) *prompter {
	// Keys pressed while setup downloaded would answer the first
	// question unseen: an Enter would pick plain HTTP.
	flushInput(int(tty.Fd()))

	return &prompter{tty: tty, in: bufio.NewReader(tty), paint: newPaint()}
}

// fit cuts s to the terminal's width less used columns, so a redrawn line
// never wraps: the cursor moves count rows, not wrapped ones.
func (p *prompter) fit(s string, used int) string {
	width, _, err := term.GetSize(int(p.tty.Fd()))
	if err != nil || width <= 0 {
		width = 80
	}
	room := max(width-used-1, 10)
	if utf8.RuneCountInString(s) <= room {
		return s
	}

	return string([]rune(s)[:room-1]) + "…"
}

type choice struct{ label, detail string }

// choose asks the question with a menu: the arrow keys move, enter picks,
// and a number picks straight away. Without a raw terminal, it asks for
// the number.
func (p *prompter) choose(question string, choices []choice) (int, error) {
	fd := int(p.tty.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return p.chooseByNumber(question, choices)
	}
	defer term.Restore(fd, state)
	// Raw mode doesn't turn \n into \r\n, so every line ends with both.
	fmt.Fprint(p.tty, "\033[?25l")
	defer fmt.Fprint(p.tty, "\033[?25h")

	cursor := 0
	fmt.Fprint(p.tty, "\r\n")
	draw := func() {
		fmt.Fprintf(p.tty, "\r\033[K  %s %s\r\n", p.paint.accent("?"), p.paint.bold(question))
		width := 0
		for _, c := range choices {
			width = max(width, utf8.RuneCountInString(c.label))
		}
		for i, c := range choices {
			label := fmt.Sprintf("%-*s", width, c.label)
			detail := p.fit(c.detail, 4+width+2)
			if i == cursor {
				fmt.Fprintf(p.tty, "\r\033[K  %s %s  %s\r\n", p.paint.accent("›"), p.paint.bold(label), detail)
			} else {
				fmt.Fprintf(p.tty, "\r\033[K    %s  %s\r\n", label, p.paint.dim(detail))
			}
		}
		fmt.Fprintf(p.tty, "\r\033[K  %s", p.paint.dim("↑↓ to move, enter to choose"))
	}
	lines := len(choices) + 1
	draw()
	buf := make([]byte, 8)
	for {
		n, err := p.tty.Read(buf)
		if err != nil {
			return 0, err
		}
		key := string(buf[:n])
		switch {
		case key == "\x1b[A" || key == "\x1bOA" || key == "k":
			cursor = (cursor + len(choices) - 1) % len(choices)
		case key == "\x1b[B" || key == "\x1bOB" || key == "j":
			cursor = (cursor + 1) % len(choices)
		case key == "\r" || key == "\n":
			p.erase(lines + 1)
			return cursor, nil
		case len(key) == 1 && key[0] >= '1' && int(key[0]-'0') <= len(choices):
			p.erase(lines + 1)
			return int(key[0] - '1'), nil
		case key == "\x03" || key == "\x04" || key == "\x1b":
			p.erase(lines + 1)
			return 0, errCancelled
		default:
			continue
		}
		fmt.Fprintf(p.tty, "\033[%dA", lines)
		draw()
	}
}

// erase clears the question, whose last line is lines below its first.
func (p *prompter) erase(lines int) {
	fmt.Fprintf(p.tty, "\r\033[%dA\033[J", lines)
}

func (p *prompter) chooseByNumber(question string, choices []choice) (int, error) {
	fmt.Fprintf(p.tty, "%s\n", question)
	for i, c := range choices {
		fmt.Fprintf(p.tty, "  %d) %s: %s\n", i+1, c.label, c.detail)
	}
	fmt.Fprint(p.tty, "Choose [1]: ")
	answer, err := p.in.ReadString('\n')
	if err != nil {
		return 0, errCancelled
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return 0, nil
	}
	for i := range choices {
		if answer == fmt.Sprint(i+1) {
			return i, nil
		}
	}

	return 0, fmt.Errorf("%q isn't one of the choices", answer)
}

// ask reads a line, hidden as it's typed if hidden, with hint under the
// prompt. It asks again, with check's complaint in place of the hint,
// until check accepts the answer.
func (p *prompter) ask(label, hint string, hidden bool, check func(string) error) (string, error) {
	fd := int(p.tty.Fd())
	note := p.paint.dim(p.fit(hint, 4))
	fmt.Fprint(p.tty, "\n")
	for {
		prompt := fmt.Sprintf("  %s %s %s ", p.paint.accent("?"), p.paint.bold(label), p.paint.dim("›"))
		// The hint goes on the next line, then the cursor comes back up to
		// the end of the prompt.
		fmt.Fprintf(p.tty, "\r\033[J\n    %s\033[1A\r%s", note, prompt)
		var answer string
		if hidden {
			secret, err := term.ReadPassword(fd)
			if err != nil {
				fmt.Fprint(p.tty, "\r\033[J")
				return "", errCancelled
			}
			answer = string(secret)
			// Nothing was echoed, not even the newline.
			fmt.Fprint(p.tty, "\r")
		} else {
			line, err := p.in.ReadString('\n')
			if err != nil {
				fmt.Fprint(p.tty, "\r\033[J")
				return "", errCancelled
			}
			answer = line
			fmt.Fprint(p.tty, "\033[1A\r")
		}
		answer = strings.TrimSpace(answer)
		err := check(answer)
		if err == nil {
			fmt.Fprint(p.tty, "\033[1A\033[J")
			return answer, nil
		}
		note = p.paint.red(p.fit(err.Error(), 4))
	}
}
