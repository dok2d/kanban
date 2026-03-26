package db

// --- Images ---

func (s *Store) SaveImage(data []byte, mime string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO images(data,mime) VALUES(?,?)", data, mime)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) GetImage(id int64) ([]byte, string, error) {
	var data []byte
	var mime string
	err := s.db.QueryRow("SELECT data,mime FROM images WHERE id=?", id).Scan(&data, &mime)
	return data, mime, err
}

// --- Files ---

func (s *Store) SaveFile(filename string, data []byte, mime string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO files(filename,data,mime,size) VALUES(?,?,?,?)", filename, data, mime, len(data))
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) GetFile(id int64) ([]byte, string, string, error) {
	var data []byte
	var mime, filename string
	err := s.db.QueryRow("SELECT data,mime,filename FROM files WHERE id=?", id).Scan(&data, &mime, &filename)
	return data, mime, filename, err
}

func (s *Store) DeleteImage(id int64) error {
	_, err := s.db.Exec("DELETE FROM images WHERE id=?", id)
	return err
}

func (s *Store) DeleteFile(id int64) error {
	_, err := s.db.Exec("DELETE FROM files WHERE id=?", id)
	return err
}

// CleanupOrphanFiles removes images and files not referenced in any comment or task description.
func (s *Store) CleanupOrphanFiles() (int, error) {
	// Collect all referenced IDs from comments and task descriptions
	refImages := make(map[int64]bool)
	refFiles := make(map[int64]bool)

	// Scan comments
	rows, err := s.db.Query("SELECT text FROM comments")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var text string
		rows.Scan(&text)
		collectFileRefs(text, refImages, refFiles)
	}
	rows.Close()

	// Scan task descriptions
	rows, err = s.db.Query("SELECT description FROM tasks WHERE description != ''")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var text string
		rows.Scan(&text)
		collectFileRefs(text, refImages, refFiles)
	}
	rows.Close()

	count := 0

	// Delete orphan images
	imgRows, err := s.db.Query("SELECT id FROM images")
	if err != nil {
		return 0, err
	}
	var orphanImgs []int64
	for imgRows.Next() {
		var id int64
		imgRows.Scan(&id)
		if !refImages[id] {
			orphanImgs = append(orphanImgs, id)
		}
	}
	imgRows.Close()
	for _, id := range orphanImgs {
		s.db.Exec("DELETE FROM images WHERE id=?", id)
		count++
	}

	// Delete orphan files
	fileRows, err := s.db.Query("SELECT id FROM files")
	if err != nil {
		return count, err
	}
	var orphanFiles []int64
	for fileRows.Next() {
		var id int64
		fileRows.Scan(&id)
		if !refFiles[id] {
			orphanFiles = append(orphanFiles, id)
		}
	}
	fileRows.Close()
	for _, id := range orphanFiles {
		s.db.Exec("DELETE FROM files WHERE id=?", id)
		count++
	}

	return count, nil
}
