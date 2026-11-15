package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/pkg/sftp"

	"sshclient/internal/oledrag"
	"sshclient/internal/sshclient"
)

// Перетаскивание файлов сервера в Проводник: виртуальные файлы OLE (internal/oledrag). Проводник
// получает список файлов и папок с размерами и читает каждый файл потоком через sftp-сессию вкладки,
// со своим окном прогресса. Пока Проводник копирует, вкладка остаётся пользователем подключения;
// закрытая вкладка или оборванное подключение обрывают копирование ошибкой «сетевое имя недоступно».

const (
	// dragOutMaxItems — сколько файлов и папок берёт одно перетаскивание; больше — через «Скачать…».
	dragOutMaxItems = 10000
	// dragOutPlanTimeout — сколько ждать обхода выбранных папок, пока кнопка мыши зажата.
	dragOutPlanTimeout = 15 * time.Second
)

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errDragOutTooMany = fmt.Errorf("Слишком много файлов для перетаскивания (больше %d): используйте «Скачать…»", dragOutMaxItems)
	errDragOutEmpty   = errors.New("Нечего перетаскивать: выбранное не прочитано")
)

// dragItems turns a plan into the descriptor items of a drag: every component of a path made a
// name Windows accepts, joined with backslashes. An entry whose path does not fit the descriptor is
// left out with everything under it; skipped counts them. srcs are the remote paths by index.
func dragItems(plan []sshclient.PlanItem) (items []oledrag.Item, srcs []string, skipped int) {
	var cut []string // the rels of folders left out
	for _, p := range plan {
		if under(p.Rel, cut) {
			skipped++
			continue
		}
		parts := strings.Split(p.Rel, "/")
		for i, c := range parts {
			parts[i], _ = sshclient.SafeLocalName(c)
		}
		rel := strings.Join(parts, `\`)
		if oledrag.CheckPath(rel) != nil {
			skipped++
			if p.Dir {
				cut = append(cut, p.Rel)
			}
			continue
		}
		items = append(items, oledrag.Item{Path: rel, Dir: p.Dir, Size: p.Size, ModTime: p.ModTime})
		srcs = append(srcs, p.Src)
	}
	return items, srcs, skipped
}

// dragPlan walks the chosen remote paths of tab t into the items of a drag.
func (t *filesTab) dragPlan(paths []string) ([]oledrag.Item, []string, error) {
	return t.dragPlanCtx(t.ctx, paths)
}

// dragPlanCtx is dragPlan that also stops when ctx ends.
func (t *filesTab) dragPlanCtx(ctx context.Context, paths []string) ([]oledrag.Item, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, dragOutPlanTimeout)
	defer cancel()
	var plan []sshclient.PlanItem
	err := t.withSFTP(func(sc *sftp.Client) error {
		var err error
		plan, err = sshclient.PlanRemote(ctx, sc, paths, func(string, error) {})
		if err == nil && len(plan) > dragOutMaxItems {
			return errDragOutTooMany
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, srcs, _ := dragItems(plan)
	if len(items) > dragOutMaxItems {
		return nil, nil, errDragOutTooMany
	}
	if len(items) == 0 {
		return nil, nil, errDragOutEmpty
	}
	return items, srcs, nil
}

// dragSource opens the files of a drag on the tab's sftp session, from whatever thread the drop
// target reads on.
type dragSource struct {
	t    *filesTab
	srcs []string
}

func (s *dragSource) Open(i int) (oledrag.File, error) {
	if i < 0 || i >= len(s.srcs) {
		return nil, fs.ErrNotExist
	}
	var f *sftp.File
	err := s.t.withSFTP(func(sc *sftp.Client) error {
		var err error
		f, err = sc.Open(s.srcs[i])
		return err
	})
	if err != nil {
		return nil, dragError(err)
	}
	return dragFile{f}, nil
}

func (s *dragSource) Release() {}

// dragFile maps the errors of a remote read for the stream: a lost session becomes oledrag.ErrLost
// with no io.EOF in its chain, so the stream never takes a broken connection for the end of a file.
type dragFile struct{ f *sftp.File }

func (d dragFile) Seek(off int64, whence int) (int64, error) {
	n, err := d.f.Seek(off, whence)
	return n, dragError(err)
}

func (d dragFile) WriteTo(w io.Writer) (int64, error) {
	n, err := d.f.WriteTo(w)
	return n, dragError(err)
}

func (d dragFile) Close() error { return d.f.Close() }

// dragError is err as the stream reports it, with only the markers it maps to an HRESULT kept in
// the chain.
func dragError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: %s", fs.ErrNotExist, err.Error())
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%w: %s", fs.ErrPermission, err.Error())
	case sshclient.IsLost(err), errors.Is(err, errFilesClosed), errors.Is(err, context.Canceled):
		return fmt.Errorf("%w: %s", oledrag.ErrLost, err.Error())
	}
	return errors.New(err.Error())
}

// dragEvent is what the window's drop target saw of a watched row drag.
type dragEvent int

const (
	dragTick    dragEvent = iota // nothing new: a periodic look at the mouse
	dragLeft                     // the drag left the window or was cancelled over it
	dragDropped                  // the drag was dropped on the window
)

// dragOutStep decides a watched row drag: out starts the system drag (the button is held with the
// cursor outside the window), end stops watching (dropped, cancelled over the window, or the button
// is up). Otherwise the HTML drag goes on inside the window.
func dragOutStep(ev dragEvent, buttonDown, outside bool) (out, end bool) {
	switch {
	case ev == dragDropped, !buttonDown:
		return false, true
	case outside:
		return true, false
	case ev == dragLeft: // left while the cursor is over the window: cancelled (Esc)
		return false, true
	}
	return false, false
}

// FilesDragOut watches the row drag of the chosen remote files and folders of a tab: the frontend
// calls it when the drag starts, and it returns when the drag ends. When the drag leaves the window
// with the button held, it ends the HTML drag and hands the files to Windows as a system drag that
// Explorer copies from in its own progress window. A drag that ends inside the window returns nil.
func (a *App) FilesDragOut(tabID string, paths []string) error {
	t, err := a.filesTab(tabID)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errFilesNothing
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "/") {
			return errBadInput
		}
	}
	return a.dragOut(t, paths)
}
