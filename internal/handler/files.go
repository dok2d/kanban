package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// --- Images ---
func (h *Handler) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	log.Printf("[upload] image: content-length=%d", r.ContentLength)
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	var req struct {
		Data string `json:"data"`
		Mime string `json:"mime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[upload] image: decode error: %v", err)
		http.Error(w, "bad request or body too large", http.StatusBadRequest)
		return
	}
	if req.Data == "" {
		http.Error(w, "data required", http.StatusBadRequest)
		return
	}
	if req.Mime == "" {
		req.Mime = "image/png"
	}
	if !allowedImageMIME[req.Mime] {
		log.Printf("[upload] image: unsupported mime: %s", req.Mime)
		http.Error(w, "unsupported image type", http.StatusBadRequest)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64", http.StatusBadRequest)
		return
	}
	// Validate content matches declared MIME type
	detectedMIME := http.DetectContentType(raw)
	if !strings.HasPrefix(detectedMIME, "image/") {
		http.Error(w, "file content is not a valid image", http.StatusBadRequest)
		return
	}
	log.Printf("[upload] image: decoded=%d bytes (%.1f MB), mime=%s", len(raw), float64(len(raw))/(1024*1024), req.Mime)
	if len(raw) > imageWarnUncompressed {
		log.Printf("[upload] image: WARNING client compression may have failed (>1MB)")
	}
	if len(raw) > maxUploadSize {
		log.Printf("[upload] image: rejected, size %d > %dMB", len(raw), maxUploadSize/(1024*1024))
		http.Error(w, fmt.Sprintf("image too large (max %dMB)", maxUploadSize/(1024*1024)), http.StatusRequestEntityTooLarge)
		return
	}
	id, err := h.store.SaveImage(raw, req.Mime)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[upload] image: saved id=%d, size=%d", id, len(raw))
	jsonResp(w, map[string]any{"id": id, "url": "/api/images/" + strconv.FormatInt(id, 10)})
}

func (h *Handler) handleImageServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := extractID(r.URL.Path, "/api/images/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	data, mime, err := h.store.GetImage(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", imageCacheMaxAge)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

// --- Files ---
func (h *Handler) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	log.Printf("[upload] file: content-length=%d", r.ContentLength)
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	var req struct {
		Data     string `json:"data"`
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[upload] file: decode error: %v", err)
		http.Error(w, "bad request or body too large", http.StatusBadRequest)
		return
	}
	if req.Data == "" || req.Filename == "" {
		http.Error(w, "data and filename required", http.StatusBadRequest)
		return
	}
	// Security: check extension
	ext := strings.ToLower(filepath.Ext(req.Filename))
	if blockedExtensions[ext] {
		log.Printf("[upload] file: blocked extension: %s", ext)
		http.Error(w, "file type not allowed", http.StatusBadRequest)
		return
	}
	// Security: check MIME
	if req.Mime == "" {
		req.Mime = "application/octet-stream"
	}
	if !allowedFileMIME[req.Mime] {
		log.Printf("[upload] file: blocked mime: %s", req.Mime)
		http.Error(w, "file MIME type not allowed", http.StatusBadRequest)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64", http.StatusBadRequest)
		return
	}
	log.Printf("[upload] file: name=%s, decoded=%d bytes (%.1f MB), mime=%s", req.Filename, len(raw), float64(len(raw))/(1024*1024), req.Mime)
	if strings.HasPrefix(req.Mime, "image/") && len(raw) > imageWarnUncompressed {
		log.Printf("[upload] file: WARNING image not compressed by client (>1MB)")
	}
	if len(raw) > maxUploadSize {
		log.Printf("[upload] file: rejected, size %d > %dMB", len(raw), maxUploadSize/(1024*1024))
		http.Error(w, fmt.Sprintf("file too large (max %dMB)", maxUploadSize/(1024*1024)), http.StatusRequestEntityTooLarge)
		return
	}
	// Validate file content matches declared MIME using magic bytes
	detectedMIME := http.DetectContentType(raw)
	if strings.HasPrefix(req.Mime, "image/") && !strings.HasPrefix(detectedMIME, "image/") {
		http.Error(w, "file content does not match declared MIME type", http.StatusBadRequest)
		return
	}
	// Sanitize filename — only allow safe characters
	safeName := filepath.Base(req.Filename)
	safeName = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, safeName)
	id, err := h.store.SaveFile(safeName, raw, req.Mime)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]any{"id": id, "url": "/api/files/" + strconv.FormatInt(id, 10), "filename": safeName})
}

func (h *Handler) handleFileServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := extractID(r.URL.Path, "/api/files/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	data, mime, filename, err := h.store.GetFile(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}
