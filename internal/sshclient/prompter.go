package sshclient

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// Prompter задаёт вопросы пользователю текстом в потоке терминала и читает ответы из того же потока.
// Работает как построчный редактор на «сыром» терминале. Безопасен для одного читателя одновременно.
type Prompter struct {
	out    io.Writer
	in     *bufio.Reader
	skipLF bool // предыдущая строка закончилась на \r: проглотить следующий \n
}

// NewPrompter создаёт Prompter: вывод идёт в out, ввод читается из in по одному символу.
func NewPrompter(out io.Writer, in io.Reader) *Prompter {
	return &Prompter{out: out, in: bufio.NewReader(in)}
}

// Ask печатает prompt и читает строку с эхом вводимых символов.
func (p *Prompter) Ask(prompt string) (string, error) { return p.readLine(prompt, true) }

// AskSecret печатает prompt и читает строку без эха; после ввода печатает перевод строки.
func (p *Prompter) AskSecret(prompt string) (string, error) { return p.readLine(prompt, false) }

// Confirm задаёт вопрос да/нет; на непонятный ответ повторяет вопрос с подсказкой.
func (p *Prompter) Confirm(prompt string) (bool, error) {
	for {
		answer, err := p.Ask(prompt)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "yes", "y", "да", "д":
			return true, nil
		case "no", "n", "нет", "н":
			return false, nil
		}
		prompt = strings.TrimSuffix(prompt, " [yes/no] ") + " [yes/no] "
	}
}

func (p *Prompter) readLine(prompt string, echo bool) (string, error) {
	if _, err := io.WriteString(p.out, prompt); err != nil {
		return "", err
	}
	var line []rune
	for {
		r, _, err := p.in.ReadRune()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", ErrCancelled
			}
			return "", err
		}
		if p.skipLF {
			p.skipLF = false
			if r == '\n' {
				continue
			}
		}
		switch {
		case r == '\r' || r == '\n':
			p.skipLF = r == '\r'
			_, err := io.WriteString(p.out, "\r\n")
			return string(line), err
		case r == 0x03:
			if _, err := io.WriteString(p.out, "^C\r\n"); err != nil {
				return "", errors.Join(ErrCancelled, err)
			}
			return "", ErrCancelled
		case r == 0x7f || r == '\b':
			if len(line) == 0 {
				continue
			}
			line = line[:len(line)-1]
			if echo {
				if _, err := io.WriteString(p.out, "\b \b"); err != nil {
					return "", err
				}
			}
		case r == 0x1b:
			if err := p.skipEscape(); err != nil {
				return "", err
			}
		case r < 0x20:
			// Остальные управляющие символы игнорируем.
		default:
			line = append(line, r)
			if echo {
				if _, err := io.WriteString(p.out, string(r)); err != nil {
					return "", err
				}
			}
		}
	}
}

// skipEscape пропускает остаток escape-последовательности (стрелки, Delete и т. п.), чтобы она не попала в ответ.
func (p *Prompter) skipEscape() error {
	r, _, err := p.in.ReadRune()
	if err != nil {
		return readErr(err)
	}
	switch r {
	case '[': // CSI: параметры, затем конечный байт 0x40-0x7E
		for {
			c, _, err := p.in.ReadRune()
			if err != nil {
				return readErr(err)
			}
			if c >= 0x40 && c <= 0x7e {
				return nil
			}
		}
	case 'O': // SS3: ещё один символ
		if _, _, err := p.in.ReadRune(); err != nil {
			return readErr(err)
		}
	default: // одиночный ESC: следующий символ обработать обычным образом
		return p.in.UnreadRune()
	}
	return nil
}

func readErr(err error) error {
	if errors.Is(err, io.EOF) {
		return ErrCancelled
	}
	return err
}
