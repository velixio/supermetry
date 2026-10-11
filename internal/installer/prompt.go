package installer

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Prompter asks line-oriented installer questions.
type Prompter struct {
	in     io.Reader
	out    io.Writer
	reader *bufio.Reader
}

// NewPrompter creates a Prompter using in and out.
func NewPrompter(in io.Reader, out io.Writer) *Prompter {
	return &Prompter{in: in, out: out, reader: bufio.NewReader(in)}
}

// Ask prompts until validate accepts the answer. An empty answer keeps def.
func (p *Prompter) Ask(label, def string, validate func(string) error) (string, error) {
	for {
		if def == "" {
			fmt.Fprintf(p.out, "%s: ", label)
		} else {
			fmt.Fprintf(p.out, "%s [%s]: ", label, def)
		}
		answer, err := p.readLine()
		if err != nil {
			return "", err
		}
		if answer == "" {
			answer = def
		}
		if validate != nil {
			if err := validate(answer); err != nil {
				fmt.Fprintln(p.out, err)
				continue
			}
		}
		return answer, nil
	}
}

// AskSecret asks for a secret without printing its current value. The project
// does not currently depend on x/term, so input uses the same plain reader.
func (p *Prompter) AskSecret(label, def string) (string, error) {
	marker := ""
	if def != "" {
		marker = " [keep]"
	}
	fmt.Fprintf(p.out, "%s%s: ", label, marker)
	answer, err := p.readLine()
	if err != nil {
		return "", err
	}
	if answer == "" {
		return def, nil
	}
	return answer, nil
}

// Choose asks for one of options.
func (p *Prompter) Choose(label string, options []string, def string) (string, error) {
	allowed := make(map[string]struct{}, len(options))
	for _, option := range options {
		allowed[option] = struct{}{}
	}
	return p.Ask(fmt.Sprintf("%s (%s)", label, strings.Join(options, "/")), def, func(value string) error {
		if _, ok := allowed[value]; !ok {
			return fmt.Errorf("choose one of: %s", strings.Join(options, ", "))
		}
		return nil
	})
}

// Confirm asks a yes/no question.
func (p *Prompter) Confirm(label string, def bool) (bool, error) {
	defaultText := "y/N"
	if def {
		defaultText = "Y/n"
	}
	for {
		fmt.Fprintf(p.out, "%s [%s]: ", label, defaultText)
		answer, err := p.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(p.out, "answer yes or no")
		}
	}
}

func (p *Prompter) readLine() (string, error) {
	if p == nil || p.in == nil {
		return "", errors.New("installer needs a prompt input")
	}
	if p.reader == nil {
		p.reader = bufio.NewReader(p.in)
	}
	line, err := p.reader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
