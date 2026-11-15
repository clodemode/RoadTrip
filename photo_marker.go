package main

// A bad photo becomes a marker (Kris, 2026-11-14: many early photos were bad).
// "Remove photo" on a signed-in photo post takes the picture off the post and
// keeps the post: the same occurred_at, tz and pin (the pin columns are never
// touched, so the fuzz seam still decides what is published). One tap, no
// dialog, and an undo window.
//
// The undo design. Removing moves the original and its thumbnail out of the
// public media path into $DATA_DIR/photo-holding/<post id>/, a folder no route
// serves, and deletes the photos row (which holds the exact GPS the photo
// carried). The row's fields wait in photo_holds with an expiry stamp, ten
// minutes out. Undo moves the files back and re-inserts the row under its old
// id. A sweep (on boot, every minute, and at the start of every removal, undo
// and /driver/posts request) deletes the held files and the hold once the
// stamp has passed; after that nothing of the photo exists on the volume. When
// a second post shares the same photo bytes (content addressed) the files and
// row are left where they are and only this post lets go of them.

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const photoHoldDir = "photo-holding"

const defaultPhotoUndoWindow = 10 * time.Minute

var (
	errNoPhoto     = errors.New("that post has no photo to remove")
	errNoHold      = errors.New("there is nothing to undo; the time may have passed")
	errUndoExpired = errors.New("the time to undo has passed")
)

// PhotoHold is a removed photo waiting out its undo window.
type PhotoHold struct {
	PostID    int64
	Photo     Photo
	Shared    bool
	ExpiresAt time.Time
}

func (a *App) photoNow() time.Time {
	if a.clockFn != nil {
		return a.clockFn()
	}
	return time.Now()
}

func (a *App) photoWindow() time.Duration {
	if a.photoUndoFor > 0 {
		return a.photoUndoFor
	}
	return defaultPhotoUndoWindow
}

func (a *App) holdFolder(postID int64) string {
	return filepath.Join(a.dataDir, photoHoldDir, strconv.FormatInt(postID, 10))
}

// mediaRel keeps a stored relative path inside the media dir.
func (a *App) mediaFile(rel string) string {
	return filepath.Join(a.mediaDir, filepath.FromSlash(rel))
}

func moveFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

// removePhoto takes the photo off a post and starts its undo window.
func (a *App) removePhoto(postID int64, now time.Time) error {
	post, err := a.store.Post(postID)
	if err != nil {
		return err
	}
	if post.Photo == nil {
		return errNoPhoto
	}
	ph := post.Photo
	shared := a.store.PhotoRefs(ph.ID, postID) > 0
	folder := a.holdFolder(postID)
	if !shared {
		for _, m := range [][2]string{{a.mediaFile(ph.Path), filepath.Join(folder, "original.jpg")}, {a.mediaFile(ph.ThumbPath), filepath.Join(folder, "thumb.jpg")}} {
			if err := moveFile(m[0], m[1]); err != nil && !errors.Is(err, os.ErrNotExist) {
				a.putBack(ph, folder)
				return fmt.Errorf("could not take the photo off the public path: %w", err)
			}
		}
	}
	if err := a.store.HoldPhoto(postID, ph, shared, now.Add(a.photoWindow())); err != nil {
		if !shared {
			a.putBack(ph, folder)
		}
		return err
	}
	return nil
}

// putBack returns whatever was already moved to its public path.
func (a *App) putBack(ph *Photo, folder string) {
	_ = moveFile(filepath.Join(folder, "original.jpg"), a.mediaFile(ph.Path))
	_ = moveFile(filepath.Join(folder, "thumb.jpg"), a.mediaFile(ph.ThumbPath))
	_ = os.Remove(folder)
}

// undoPhoto brings a removed photo back while its window is open.
func (a *App) undoPhoto(postID int64, now time.Time) error {
	a.sweepPhotoHolds(now)
	h, err := a.store.HeldPhoto(postID)
	if err != nil {
		return errNoHold
	}
	if !now.Before(h.ExpiresAt) {
		return errUndoExpired
	}
	post, err := a.store.Post(postID)
	if err != nil || post.Photo != nil {
		return errNoHold
	}
	folder := a.holdFolder(postID)
	reuse := int64(0)
	if !h.Shared {
		if cur, err := a.store.PhotoBySHA(h.Photo.SHA256); err == nil {
			// The same bytes were posted again in the window: that row serves.
			reuse = cur.ID
		} else {
			if err := moveFile(filepath.Join(folder, "original.jpg"), a.mediaFile(h.Photo.Path)); err != nil {
				return fmt.Errorf("could not bring the photo back: %w", err)
			}
			if err := moveFile(filepath.Join(folder, "thumb.jpg"), a.mediaFile(h.Photo.ThumbPath)); err != nil {
				return fmt.Errorf("could not bring the photo back: %w", err)
			}
		}
	}
	if err := a.store.RestoreHeld(postID, reuse); err != nil {
		return err
	}
	_ = os.RemoveAll(folder)
	return nil
}

// sweepPhotoHolds deletes every hold whose window has closed, files and all.
func (a *App) sweepPhotoHolds(now time.Time) int {
	holds, err := a.store.ExpiredHolds(now)
	if err != nil {
		log.Printf("WARN photo holds: %v", err)
		return 0
	}
	for _, h := range holds {
		if !h.Shared {
			if err := os.RemoveAll(a.holdFolder(h.PostID)); err != nil {
				log.Printf("WARN photo hold %d: %v", h.PostID, err)
				continue // keep the row so the next sweep tries again
			}
		}
		_ = a.store.DropHold(h.PostID)
	}
	return len(holds)
}

// heldFor is the minutes left to undo a removed photo on a post, 0 for none.
func (a *App) heldFor(postID int64) int {
	h, err := a.store.HeldPhoto(postID)
	if err != nil {
		return 0
	}
	left := h.ExpiresAt.Sub(a.photoNow())
	if left <= 0 {
		return 0
	}
	return int(left/time.Minute) + 1
}

// --- store ----------------------------------------------------------------------

// PhotoRefs counts the posts other than one that carry a photo.
func (s *Store) PhotoRefs(photoID, exceptPost int64) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM posts WHERE photo_id=? AND id<>?`, photoID, exceptPost).Scan(&n)
	return n
}

func (s *Store) HoldPhoto(postID int64, ph *Photo, shared bool, expires time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR REPLACE INTO photo_holds
	  (post_id,photo_id,path,thumb_path,taken_at,tz,lat,lng,width,height,sha256,shared,expires_at)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		postID, ph.ID, ph.Path, ph.ThumbPath, ph.TakenAt, ph.TZ, ph.Lat, ph.Lng, ph.Width, ph.Height, ph.SHA256,
		shared, utcString(expires)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE posts SET photo_id=NULL WHERE id=?`, postID); err != nil {
		return err
	}
	if !shared {
		if _, err := tx.Exec(`DELETE FROM photos WHERE id=?`, ph.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) HeldPhoto(postID int64) (*PhotoHold, error) {
	rows, err := s.heldRows(`WHERE post_id=?`, postID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNoHold
	}
	return &rows[0], nil
}

func (s *Store) ExpiredHolds(now time.Time) ([]PhotoHold, error) {
	return s.heldRows(`WHERE expires_at <= ?`, utcString(now))
}

func (s *Store) HeldAll() ([]PhotoHold, error) { return s.heldRows(`WHERE 1=1`) }

func (s *Store) heldRows(where string, args ...any) ([]PhotoHold, error) {
	rows, err := s.db.Query(`SELECT post_id,photo_id,path,thumb_path,taken_at,tz,lat,lng,width,height,sha256,shared,expires_at
	  FROM photo_holds `+where+` ORDER BY post_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhotoHold
	for rows.Next() {
		var h PhotoHold
		var exp string
		if err := rows.Scan(&h.PostID, &h.Photo.ID, &h.Photo.Path, &h.Photo.ThumbPath, &h.Photo.TakenAt, &h.Photo.TZ,
			&h.Photo.Lat, &h.Photo.Lng, &h.Photo.Width, &h.Photo.Height, &h.Photo.SHA256, &h.Shared, &exp); err != nil {
			return nil, err
		}
		h.ExpiresAt, _ = time.Parse(time.RFC3339, exp)
		out = append(out, h)
	}
	return out, rows.Err()
}

// RestoreHeld gives a post its photo back. reuse is the id of a photos row that
// already holds the same bytes (posted again in the window); 0 re-inserts the
// held row under its old id.
func (s *Store) RestoreHeld(postID, reuse int64) error {
	h, err := s.HeldPhoto(postID)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := reuse
	if id == 0 {
		id = h.Photo.ID
		if _, err := tx.Exec(`INSERT OR IGNORE INTO photos (id,path,thumb_path,taken_at,tz,lat,lng,width,height,sha256)
		  VALUES (?,?,?,?,?,?,?,?,?,?)`, id, h.Photo.Path, h.Photo.ThumbPath, h.Photo.TakenAt, h.Photo.TZ,
			h.Photo.Lat, h.Photo.Lng, h.Photo.Width, h.Photo.Height, h.Photo.SHA256); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE posts SET photo_id=? WHERE id=?`, id, postID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM photo_holds WHERE post_id=?`, postID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DropHold(postID int64) error {
	_, err := s.db.Exec(`DELETE FROM photo_holds WHERE post_id=?`, postID)
	return err
}

// --- handlers -------------------------------------------------------------------

func photoNotice(err error) string {
	switch {
	case err == nil:
		return "Photo removed. The post stays as a marker. You can undo for a few minutes."
	case errors.Is(err, errUndoExpired):
		return "The time to undo has passed; the photo is gone."
	default:
		return err.Error() + "."
	}
}

func (a *App) photoBack(w http.ResponseWriter, r *http.Request, err error) {
	dest := backTo(r, "/driver/posts")
	sep := "?"
	if u, perr := url.Parse(dest); perr == nil && u.RawQuery != "" {
		sep = "&"
	}
	http.Redirect(w, r, dest+sep+"notice="+url.QueryEscape(photoNotice(err)), http.StatusSeeOther)
}

func (a *App) postIDs(r *http.Request) []int64 {
	_ = r.ParseForm()
	var ids []int64
	for _, v := range r.PostForm["post_id"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// photoRemove: POST /driver/post/{id}/photo/remove
func (a *App) photoRemove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	now := a.photoNow()
	a.sweepPhotoHolds(now)
	a.photoBack(w, r, a.removePhoto(id, now))
}

// photoUndo: POST /driver/post/{id}/photo/undo
func (a *App) photoUndo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = a.undoPhoto(id, a.photoNow())
	if err == nil {
		http.Redirect(w, r, backTo(r, "/driver/posts")+"?notice="+url.QueryEscape("Photo back."), http.StatusSeeOther)
		return
	}
	a.photoBack(w, r, err)
}

// photosRemove: POST /driver/posts/photos/remove, the ticked posts of /driver/posts.
func (a *App) photosRemove(w http.ResponseWriter, r *http.Request) {
	now := a.photoNow()
	a.sweepPhotoHolds(now)
	n := 0
	var last error
	for _, id := range a.postIDs(r) {
		if err := a.removePhoto(id, now); err != nil {
			last = err
			continue
		}
		n++
	}
	msg := fmt.Sprintf("%d photo%s removed. The posts stay as markers. You can undo for a few minutes.", n, plural(n))
	if n == 0 && last != nil {
		msg = photoNotice(last)
	}
	http.Redirect(w, r, "/driver/posts?notice="+url.QueryEscape(msg), http.StatusSeeOther)
}

// photosUndo: POST /driver/posts/photos/undo, every removed photo still in its window.
func (a *App) photosUndo(w http.ResponseWriter, r *http.Request) {
	now := a.photoNow()
	holds, _ := a.store.HeldAll()
	n := 0
	for _, h := range holds {
		if a.undoPhoto(h.PostID, now) == nil {
			n++
		}
	}
	http.Redirect(w, r, "/driver/posts?notice="+url.QueryEscape(fmt.Sprintf("%d photo%s back.", n, plural(n))), http.StatusSeeOther)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

type heldView struct {
	PostID int64
	Title  string
	When   string
	Mins   int
}

// photosPage: GET /driver/posts, the ride's photo posts with a checkbox each.
func (a *App) photosPage(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.sweepPhotoHolds(a.photoNow())
	posts, err := a.store.Posts(rv.Ride.Slug, 0)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	var withPhoto []Post
	var held []heldView
	for _, p := range posts {
		if p.Photo != nil {
			withPhoto = append(withPhoto, p)
		} else if m := a.heldFor(p.ID); m > 0 {
			held = append(held, heldView{PostID: p.ID, Title: p.Title, When: localFmt(p.OccurredAt, p.TZ, fmtLocal), Mins: m})
		}
	}
	pageLeg := ""
	if len(rv.Legs) > 0 {
		pageLeg = rv.Legs[0].Slug
	}
	a.render(w, r, "posts_admin.html", map[string]any{
		"Title": "Remove photos", "Ride": rv.Ride, "PageLeg": pageLeg,
		"Posts": withPhoto, "Held": held, "Notice": r.URL.Query().Get("notice")})
}
