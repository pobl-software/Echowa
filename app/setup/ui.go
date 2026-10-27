package setup

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// ui draws setup's progress. On a terminal each step shows a spinner while
// it runs, then becomes a ✓ line with what it did and how long it took.
// Anywhere else, such as the updater's log, it writes one plain line per
// finished step, with no escapes and no indent.
//
// It is also the io.Writer for commands' output, which clears the spinner
// line before writing.
type ui struct {
	mu    sync.Mutex
	out   io.Writer
	paint paint
	// live means out is a terminal that understands \r and erasing lines.
	live  bool
	width int
	step  *step
	// midLine is set when the last write didn't end its line.
	midLine bool
}

// step is the one running now.
type step struct {
	label   string
	doing   string
	started time.Time
	stop    chan struct{}
	stopped chan struct{}
}

// labelWidth fits the longest step label, "Docker Engine", with room.
const labelWidth = 14

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newUI(out io.Writer) *ui {
	u := &ui{out: out, width: 80}
	if f, ok := out.(*os.File); ok && term.IsTerminal(int(f.Fd())) && os.Getenv("TERM") != "dumb" {
		u.live = true
		u.paint = newPaint()
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			u.width = w
		}
	}

	return u
}

// banner opens the output: the logo, the version and the machine.
func (u *ui) banner(version, machine string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.live {
		fmt.Fprintf(u.out, "Echowa %s setup on %s\n", version, machine)
		return
	}
	p := u.paint
	// The logo's three bars, a signal and its fading echoes, in half blocks.
	fmt.Fprintf(u.out, "\n  %s   %s %s\n  %s   %s\n  %s   %s\n\n",
		p.accent("█ ▄  "), p.bold("Echowa"), p.dim(version),
		p.accent("█ █ █"), "Deploys for one server",
		p.accent("█ ▀  "), p.dim(machine))
}

// begin starts a step, labelled with what it sets up ("Docker Engine").
func (u *ui) begin(label string) {
	u.mu.Lock()
	if u.step != nil {
		u.mu.Unlock()
		u.end("✓", "", nil)
		u.mu.Lock()
	}
	u.step = &step{label: label, started: time.Now(), stop: make(chan struct{}), stopped: make(chan struct{})}
	if !u.live {
		u.mu.Unlock()
		return
	}
	st := u.step
	u.drawLocked()
	u.mu.Unlock()
	go u.spin(st)
}

func (u *ui) spin(st *step) {
	defer close(st.stopped)
	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-st.stop:
			return
		case <-tick.C:
			u.mu.Lock()
			u.drawLocked()
			u.mu.Unlock()
		}
	}
}

// doing says what the running step is up to ("installing").
func (u *ui) doing(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.step == nil {
		return
	}
	u.step.doing = fmt.Sprintf(format, args...)
	if u.live {
		u.drawLocked()
		return
	}
	// Off a terminal there's no spinner, so a log can say which step it's in.
	fmt.Fprintf(u.out, "· %s: %s\n", u.step.label, u.step.doing)
}

// ok ends the running step with what it did.
func (u *ui) ok(format string, args ...any) {
	u.end("✓", fmt.Sprintf(format, args...), u.paint.green)
}

// problem ends the running step with a warning: it carried on regardless.
func (u *ui) problem(format string, args ...any) {
	u.end("!", fmt.Sprintf(format, args...), u.paint.yellow)
}

// failed ends the running step, if any, then explains err.
func (u *ui) failed(err error, interrupted bool) {
	u.end("✗", "failed", u.paint.red)
	u.mu.Lock()
	defer u.mu.Unlock()
	p := u.paint
	lines := strings.Split(err.Error(), "\n")
	headline := "Setup stopped: " + lines[0]
	if interrupted {
		headline, lines = "Setup interrupted", append([]string{""}, lines...)
	}
	fmt.Fprintf(u.out, "\n%s%s %s\n", u.indent(), p.red("✗"), p.bold(headline))
	for _, line := range lines[1:] {
		if interrupted {
			line = p.dim(line)
		}
		fmt.Fprintf(u.out, "%s  %s\n", u.indent(), line)
	}
	advice := "Every step checks before it changes anything, so fix that, then run the same command again."
	if interrupted {
		advice = "Every step checks before it changes anything, so run the same command again to finish."
	}
	fmt.Fprintf(u.out, "\n%s%s\n", u.indent(), p.dim(advice))
}

// cancelled says the questions were left unanswered, before any step
// changed anything.
func (u *ui) cancelled() {
	fmt.Fprintln(u)
	u.say("Cancelled. Nothing was installed.")
}

func (u *ui) end(mark, detail string, colour func(string) string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := u.step
	if st == nil {
		return
	}
	u.step = nil
	if u.live {
		close(st.stop)
		u.mu.Unlock()
		<-st.stopped
		u.mu.Lock()
	}
	u.newLineLocked(true)
	if colour == nil {
		colour = u.paint.green
	}
	fmt.Fprintln(u.out, u.lineLocked(colour(mark), st.label, detail, time.Since(st.started), time.Second))
}

// fact is a finished step that took no time, such as an answer.
func (u *ui) fact(label, format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.newLineLocked(u.step != nil)
	fmt.Fprintln(u.out, u.lineLocked(u.paint.green("✓"), label, fmt.Sprintf(format, args...), 0, time.Second))
}

// say is a line of its own, under the running step if there is one.
func (u *ui) say(format string, args ...any) {
	u.lines(u.paint.dim("·"), fmt.Sprintf(format, args...), u.paint.dim)
}

// warn is a line to read before carrying on.
func (u *ui) warn(format string, args ...any) {
	u.lines(u.paint.yellow("!"), fmt.Sprintf(format, args...), nil)
}

func (u *ui) lines(mark, text string, colour func(string) string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.newLineLocked(u.step != nil)
	for i, line := range strings.Split(text, "\n") {
		if colour != nil {
			line = colour(line)
		}
		if i == 0 {
			fmt.Fprintf(u.out, "%s%s %s\n", u.indent(), mark, line)
		} else {
			fmt.Fprintf(u.out, "%s  %s\n", u.indent(), line)
		}
	}
	u.drawLocked()
}

// card is the summary at the end: a headline, then lines, with the
// accent bar down the left on a terminal. Lines starting with a tab are
// links, shown bold.
func (u *ui) card(headline string, lines ...string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	p := u.paint
	bar := ""
	if u.live {
		bar = p.accent("▌") + " "
	}
	fmt.Fprintf(u.out, "\n%s%s%s\n", u.indent(), bar, p.bold(headline))
	if len(lines) > 0 && u.live {
		fmt.Fprintf(u.out, "%s%s\n", u.indent(), strings.TrimRight(bar, " "))
	}
	for _, line := range lines {
		if link, ok := strings.CutPrefix(line, "\t"); ok {
			line = p.underline(p.bold(link))
		} else {
			line = p.dim(line)
		}
		fmt.Fprintf(u.out, "%s%s%s\n", u.indent(), bar, line)
	}
}

// list is a list under a heading, numbered if there's more than one item,
// wrapped to the terminal.
func (u *ui) list(heading string, items []string) {
	if len(items) == 0 {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.out, "\n%s%s\n", u.indent(), u.paint.bold(heading))
	for i, item := range items {
		number := "•"
		if len(items) > 1 {
			number = fmt.Sprintf("%d.", i+1)
		}
		hang := strings.Repeat(" ", utf8.RuneCountInString(number))
		for j, line := range u.wrap(item, len(u.indent())+len(hang)+1) {
			if j > 0 {
				number = hang
			}
			fmt.Fprintf(u.out, "%s%s %s\n", u.indent(), u.paint.accent(number), line)
		}
	}
}

// Write is for commands' output, which goes under the spinner line.
func (u *ui) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.midLine {
		u.newLineLocked(u.step != nil)
	}
	n, err := u.out.Write(p)
	if n > 0 {
		u.midLine = p[n-1] != '\n'
	}

	return n, err
}

func (u *ui) indent() string {
	if u.live {
		return "  "
	}
	return ""
}

// drawLocked redraws the spinner line.
func (u *ui) drawLocked() {
	if !u.live || u.step == nil {
		return
	}
	if u.midLine {
		fmt.Fprintln(u.out)
		u.midLine = false
	}
	st := u.step
	elapsed := time.Since(st.started)
	frame := spinner[int(elapsed/(80*time.Millisecond))%len(spinner)]
	detail := st.doing
	if detail != "" {
		detail += "…"
	}
	fmt.Fprint(u.out, "\r\033[K"+u.lineLocked(u.paint.accent(frame), st.label, detail, elapsed, 2*time.Second))
}

// newLineLocked gets the cursor to the start of an empty line, ending
// commands' output if it stopped mid-line, or erasing the spinner line if
// spinning.
func (u *ui) newLineLocked(spinning bool) {
	switch {
	case u.midLine:
		fmt.Fprintln(u.out)
		u.midLine = false
	case u.live && spinning:
		fmt.Fprint(u.out, "\r\033[K")
	}
}

// lineLocked lays out a step line: mark, label, detail, and the time
// taken once it's worth showing. A live line is cut to the terminal's
// width, so redrawing it never wraps.
func (u *ui) lineLocked(mark, label, detail string, took, showAfter time.Duration) string {
	timing := ""
	if took >= showAfter {
		timing = duration(took)
	}
	room := u.width - len(u.indent()) - 2 - labelWidth - 2 - len(timing) - 2
	if u.live && utf8.RuneCountInString(detail) > room {
		detail = string([]rune(detail)[:max(room-1, 0)]) + "…"
	}
	line := u.indent() + mark + " " + fmt.Sprintf("%-*s", labelWidth, label) + " " + u.paint.dim(detail)
	if timing != "" {
		line += "  " + u.paint.dim(timing)
	}

	return line
}

// wrap breaks text into lines that fit the terminal after hang columns.
// Off a terminal it doesn't wrap, since whatever shows it will.
func (u *ui) wrap(text string, hang int) []string {
	if !u.live {
		return []string{text}
	}
	limit := max(u.width-hang-1, 30)
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > limit {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}

	return append(lines, line)
}

func duration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// paint styles text with terminal escapes, or leaves it alone.
type paint struct{ on, truecolor bool }

// newPaint is for a terminal, unless NO_COLOR is set (no-color.org).
func newPaint() paint {
	colorterm := os.Getenv("COLORTERM")

	return paint{on: os.Getenv("NO_COLOR") == "", truecolor: colorterm == "truecolor" || colorterm == "24bit"}
}

func (p paint) style(code, s string) string {
	if !p.on || s == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// accent is the logo's burnt orange (docs/design.md), for marks and
// glyphs rather than text, which it's too dark for on a dark terminal.
func (p paint) accent(s string) string {
	if p.truecolor {
		return p.style("38;2;209;68;0", s)
	}
	return p.style("38;5;166", s)
}

func (p paint) bold(s string) string      { return p.style("1", s) }
func (p paint) dim(s string) string       { return p.style("90", s) }
func (p paint) underline(s string) string { return p.style("4", s) }
func (p paint) green(s string) string     { return p.style("32", s) }
func (p paint) yellow(s string) string    { return p.style("33", s) }
func (p paint) red(s string) string       { return p.style("31", s) }
