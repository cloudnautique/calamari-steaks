package bridge

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxUploadSize = 16 << 20
const uploadChunkSize = 192 << 10

type uploadInput struct {
	Action   string `json:"action" jsonschema:"begin, chunk, finish or cancel"`
	UploadID string `json:"uploadId,omitempty"`
	BoxID    string `json:"boxId,omitempty"`
	Path     string `json:"path,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Offset   int64  `json:"offset,omitempty"`
	Data     string `json:"data,omitempty"`
}
type fileUpload struct {
	mu           sync.Mutex
	file         *os.File
	box, path    string
	size, offset int64
	closed       bool
	timer        *time.Timer
}

func (s *Server) CloseFileUploads() {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	for id, u := range s.uploads {
		s.removeUpload(id, u)
	}
}

// uploadMu must be held. Every temporary upload is private and expires.
func (s *Server) removeUpload(id string, u *fileUpload) {
	delete(s.uploads, id)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
	if u.timer != nil {
		u.timer.Stop()
	}
	u.file.Close()
	os.Remove(u.file.Name())
}
func (s *Server) uploadFile(ctx context.Context, in uploadInput) (map[string]any, error) {
	if in.Action == "begin" {
		if in.Size < 0 || in.Size > maxUploadSize {
			return nil, errors.New("upload limit is 16 MiB per file")
		}
		if _, err := discobox.FileURI(s.Client.Session.Project, in.BoxID, in.Path); err != nil {
			return nil, err
		}
		if err := s.Client.BoxAccess(ctx, in.BoxID, s.managerOptions.Parent); err != nil {
			return nil, err
		}
		s.uploadMu.Lock()
		defer s.uploadMu.Unlock()
		if len(s.uploads) >= 4 {
			return nil, errors.New("four uploads are already active; wait for one to finish")
		}
		var key [16]byte
		if _, err := rand.Read(key[:]); err != nil {
			return nil, err
		}
		f, err := os.CreateTemp("", "discobox-upload-")
		if err != nil {
			return nil, errors.New("could not stage a private upload")
		}
		id := hex.EncodeToString(key[:])
		u := &fileUpload{file: f, box: in.BoxID, path: in.Path, size: in.Size}
		if s.uploads == nil {
			s.uploads = make(map[string]*fileUpload)
		}
		s.uploads[id] = u
		u.timer = time.AfterFunc(5*time.Minute, func() {
			s.uploadMu.Lock()
			defer s.uploadMu.Unlock()
			if s.uploads[id] == u {
				s.removeUpload(id, u)
			}
		})
		return map[string]any{"uploadId": id, "chunkSize": uploadChunkSize}, nil
	}
	s.uploadMu.Lock()
	u := s.uploads[in.UploadID]
	s.uploadMu.Unlock()
	if u == nil {
		return nil, errors.New("upload expired or ended; no upload was retried")
	}
	if in.Action == "cancel" {
		s.uploadMu.Lock()
		if s.uploads[in.UploadID] == u {
			s.removeUpload(in.UploadID, u)
		}
		s.uploadMu.Unlock()
		return map[string]any{"cancelled": true}, nil
	}
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return nil, errors.New("upload is closed")
	}
	if in.Action == "chunk" {
		defer u.mu.Unlock()
		if in.Offset != u.offset || len(in.Data) > base64.StdEncoding.EncodedLen(uploadChunkSize) {
			return nil, errors.New("invalid upload chunk offset or size; nothing was retried")
		}
		data, err := base64.StdEncoding.DecodeString(in.Data)
		if err != nil || len(data) == 0 || len(data) > uploadChunkSize || u.offset+int64(len(data)) > u.size {
			return nil, errors.New("invalid upload chunk")
		}
		n, err := u.file.Write(data)
		u.offset += int64(n)
		if err != nil {
			return nil, errors.New("could not stage upload chunk; cancel this upload")
		}
		u.timer.Reset(5 * time.Minute)
		return map[string]any{"offset": u.offset}, nil
	}
	u.mu.Unlock()
	if in.Action != "finish" {
		return nil, errors.New("upload action is invalid")
	}
	// Detach before network I/O, in removeUpload's lock order, so nothing is
	// held during the commit: a concurrent cancel, the expiry timer and
	// shutdown cleanup no longer find this upload and cannot stall behind it.
	// Marked consumed first; uncertain commits are never replayed.
	s.uploadMu.Lock()
	u.mu.Lock()
	if s.uploads[in.UploadID] != u || u.closed || u.offset != u.size {
		u.mu.Unlock()
		s.uploadMu.Unlock()
		return nil, errors.New("upload is incomplete or closed")
	}
	delete(s.uploads, in.UploadID)
	u.closed = true
	u.timer.Stop()
	u.mu.Unlock()
	s.uploadMu.Unlock()
	defer func() {
		u.file.Close()
		os.Remove(u.file.Name())
	}()
	if _, err := u.file.Seek(0, 0); err != nil {
		return nil, errors.New("could not read staged upload")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	info, err := s.Client.WriteFile(ctx, u.box, s.managerOptions.Parent, u.path, u.file, u.size)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": info}, nil
}
func (s *Server) fileUploadTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "file_upload", Description: "App-only private file upload in bounded chunks. Never request file bytes in chat. Existing files are refused; uncertain writes are not retried.", Meta: mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}}, func(ctx context.Context, _ *mcp.CallToolRequest, in uploadInput) (*mcp.CallToolResult, any, error) {
		data, err := s.uploadFile(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Meta: mcp.Meta{"upload": data}, Content: []mcp.Content{&mcp.TextContent{Text: "Private file upload response for the app."}}}, nil, nil
	})
}
