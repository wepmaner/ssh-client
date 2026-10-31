package app

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

const errDataFolder = "Этот путь указывает на папку данных программы, где лежат ключи и хранилище. Передача файлов с неё и на неё запрещена: выберите другую папку, например «Документы» или «Загрузки»."

// checkLocalPath отказывает, если path ведёт в папку данных dataDir
// или не может быть надёжно проверен. Путь может ещё не существовать
// (цель скачивания): тогда сверяется ближайший существующий родитель.
func checkLocalPath(dataDir, path string) error {
	if strings.HasPrefix(path, `\?\`) || strings.HasPrefix(path, `\.\`) ||
		strings.HasPrefix(path, `//?/`) || strings.HasPrefix(path, `//./`) {
		return errors.New(`Пути вида \\?\ и \\.\ не поддерживаются: укажите обычный путь, например C:\Users\имя\Загрузки\файл.txt.`)
	}
	if !filepath.IsAbs(path) {
		return errors.New(`Нужен полный путь к файлу, например C:\Users\имя\Загрузки\файл.txt: относительные пути не принимаются.`)
	}
	clean := filepath.Clean(path)
	rest := strings.TrimPrefix(clean, filepath.VolumeName(clean))
	if strings.Contains(rest, ":") {
		return errors.New("Путь содержит двоеточие после имени диска (альтернативный поток NTFS). Укажите обычный путь к файлу.")
	}
	for _, c := range strings.Split(rest, string(filepath.Separator)) {
		if c != "" && (strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ")) {
			return fmt.Errorf("Имя %q оканчивается точкой или пробелом: Windows отбрасывает их, и путь может указать на другой файл. Уберите их из пути.", c)
		}
	}

	root, err := resolveNearest(dataDir)
	if err != nil {
		return fmt.Errorf("Не удалось проверить папку данных, передача файлов отклонена: %w", err)
	}
	real, err := resolveNearest(clean)
	if err != nil {
		return fmt.Errorf("Не удалось проверить путь %q, передача файла отклонена: %w. Проверьте, что путь существует и доступен.", path, err)
	}
	if within(root, real) {
		return errors.New(errDataFolder)
	}
	return nil
}

// resolveNearest раскрывает ссылки и короткие имена у ближайшего
// существующего родителя и дописывает несуществующий хвост.
func resolveNearest(p string) (string, error) {
	p = filepath.Clean(p)
	tail := ""
	cur := p
	for {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(r, tail), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}

// within сообщает, лежит ли p в root или совпадает с ним, сравнивая
// компоненты без учёта регистра.
func within(root, p string) bool {
	rc := splitPath(root)
	pc := splitPath(p)
	if len(pc) < len(rc) {
		return false
	}
	for i := range rc {
		if !strings.EqualFold(rc[i], pc[i]) {
			return false
		}
	}
	return true
}

func splitPath(p string) []string {
	var out []string
	for _, c := range strings.Split(filepath.ToSlash(filepath.Clean(p)), "/") {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}
