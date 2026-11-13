package discobox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const MaxFileChunk = 256 << 10
const MaxResourceFile = 1 << 20

type FileInfo struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	Version    string `json:"version"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	URI        string `json:"uri"`
	MIMEType   string `json:"mimeType"`
}
type FileList struct {
	BoxID   string     `json:"boxId"`
	Path    string     `json:"path"`
	Entries []FileInfo `json:"entries"`
}
type FileRead struct {
	BoxID      string   `json:"boxId"`
	File       FileInfo `json:"file"`
	Offset     int64    `json:"offset"`
	NextOffset int64    `json:"nextOffset"`
	Truncated  bool     `json:"truncated"`
	Text       *string  `json:"text,omitempty"`
	Blob       string   `json:"blob,omitempty"`
}

func cleanFilePath(value string) (string, error) {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00") || !strings.HasPrefix(value, "/") {
		return "", errors.New("file path must be an absolute box path of at most 4096 bytes")
	}
	return path.Clean(value), nil
}
func fileMIME(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".pdf":
		return "application/pdf"
	}
	return "text/plain"
}
func FileURI(project, boxID, filePath string) (string, error) {
	if !identifier.MatchString(project) || !identifier.MatchString(boxID) {
		return "", errors.New("invalid file project or box ID")
	}
	clean, err := cleanFilePath(filePath)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "discobox-file", Host: project, Path: "/" + boxID + clean}).String(), nil
}
func ParseFileURI(raw, project string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "discobox-file" || u.Host != project || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", "", errors.New("invalid file resource URI for this configured project")
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) != 2 || !identifier.MatchString(parts[0]) {
		return "", "", errors.New("invalid box file URI")
	}
	clean, err := cleanFilePath("/" + parts[1])
	if err != nil {
		return "", "", err
	}
	canonical, err := FileURI(project, parts[0], clean)
	if err != nil || canonical != raw {
		return "", "", errors.New("file URI must use its canonical encoded path")
	}
	return parts[0], clean, nil
}
func (c *Client) fileRoot(ctx context.Context, id, parent string) (string, error) {
	box, err := c.managerBox(ctx, id, parent)
	if err != nil {
		return "", err
	}
	if err := managerLifecycleError(box); err != nil {
		return "", err
	}
	// File access is independent of primary exec discovery or terminal attachment.
	return "/api/projects/" + c.Session.Project + "/sandboxes/" + id + "/files", nil
}
func (c *Client) decorateFile(info *FileInfo) error {
	clean, err := cleanFilePath(info.Path)
	if err != nil {
		return err
	}
	info.Path = clean
	if info.Name == "" {
		info.Name = path.Base(clean)
	}
	if info.Size < 0 {
		return errors.New("invalid file size from Discobox")
	}
	info.MIMEType = fileMIME(info.Name)
	return nil
}
func (c *Client) statFile(ctx context.Context, root, id, target string) (FileInfo, error) {
	result, err := c.request(ctx, http.MethodGet, root+"/stat?"+url.Values{"path": {target}}.Encode(), nil)
	if err != nil {
		return FileInfo{}, err
	}
	encoded, _ := json.Marshal(result)
	var info FileInfo
	if err := json.Unmarshal(encoded, &info); err != nil {
		return info, err
	}
	if err := c.decorateFile(&info); err != nil {
		return info, err
	}
	if info.Path != target {
		return info, errors.New("file stat returned a different path")
	}
	info.URI, err = FileURI(c.Session.Project, id, info.Path)
	return info, err
}
func (c *Client) StatFile(ctx context.Context, id, parent, target string) (FileInfo, error) {
	clean, err := cleanFilePath(target)
	if err != nil {
		return FileInfo{}, err
	}
	root, err := c.fileRoot(ctx, id, parent)
	if err != nil {
		return FileInfo{}, err
	}
	return c.statFile(ctx, root, id, clean)
}
func (c *Client) ListFiles(ctx context.Context, id, parent, target string) (FileList, error) {
	if target == "" {
		target = "/workspace"
	}
	clean, err := cleanFilePath(target)
	if err != nil {
		return FileList{}, err
	}
	root, err := c.fileRoot(ctx, id, parent)
	if err != nil {
		return FileList{}, err
	}
	raw, err := c.request(ctx, http.MethodGet, root+"/list?"+url.Values{"path": {clean}}.Encode(), nil)
	if err != nil {
		return FileList{}, err
	}
	if _, ok := raw["entries"]; !ok {
		return FileList{}, errors.New("invalid files listing response")
	}
	encoded, _ := json.Marshal(raw)
	var result FileList
	if err := json.Unmarshal(encoded, &result); err != nil {
		return result, err
	}
	if result.Path != clean {
		return result, errors.New("directory listing returned a different path")
	}
	result.BoxID = id
	if result.Entries == nil {
		result.Entries = []FileInfo{}
	}
	for i := range result.Entries {
		info := &result.Entries[i]
		if info.Name == "" || info.Name == "." || info.Name == ".." || strings.ContainsAny(info.Name, "/\\\x00") || info.Path != path.Join(clean, info.Name) {
			return FileList{}, errors.New("unsafe entry in directory listing")
		}
		if err := c.decorateFile(info); err != nil {
			return FileList{}, err
		}
		info.URI, err = FileURI(c.Session.Project, id, info.Path)
		if err != nil {
			return FileList{}, err
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if (a.Type == "directory") != (b.Type == "directory") {
			return a.Type == "directory"
		}
		return a.Name < b.Name
	})
	return result, nil
}
func (c *Client) ReadFile(ctx context.Context, id, parent, target string, offset int64, length int) (FileRead, error) {
	clean, err := cleanFilePath(target)
	if err != nil {
		return FileRead{}, err
	}
	if length == 0 {
		length = 64 << 10
	}
	if offset < 0 || length < 1 || length > MaxFileChunk {
		return FileRead{}, fmt.Errorf("file range must have a nonnegative offset and length 1 to %d", MaxFileChunk)
	}
	root, err := c.fileRoot(ctx, id, parent)
	if err != nil {
		return FileRead{}, err
	}
	info, err := c.statFile(ctx, root, id, clean)
	if err != nil {
		return FileRead{}, err
	}
	if info.Type != "file" {
		return FileRead{}, errors.New("preview supports regular files; directory and symlink contents are not read")
	}
	if offset > info.Size {
		return FileRead{}, errors.New("file offset exceeds its size")
	}
	query := url.Values{"path": {clean}, "offset": {fmt.Sprint(offset)}, "length": {fmt.Sprint(length)}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+root+"/content?"+query.Encode(), nil)
	if err != nil {
		return FileRead{}, err
	}
	request.Header = c.headers()
	response, err := c.http.Do(request)
	if err != nil {
		return FileRead{}, fmt.Errorf("file content request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return FileRead{}, &HTTPError{Status: response.StatusCode, Path: root + "/content"}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(length)+1))
	if err != nil {
		return FileRead{}, err
	}
	if len(data) > length {
		return FileRead{}, errors.New("file API exceeded the requested byte limit")
	}
	after, err := c.statFile(ctx, root, id, clean)
	if err != nil {
		return FileRead{}, err
	}
	if after.Version != info.Version || after.Size != info.Size {
		return FileRead{}, errors.New("file changed while reading; read the new version before using this content")
	}
	result := FileRead{BoxID: id, File: info, Offset: offset, NextOffset: offset + int64(len(data))}
	result.Truncated = result.NextOffset < info.Size
	if len(data) == 0 && result.Truncated {
		return FileRead{}, errors.New("file API returned an incomplete empty range")
	}
	if utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
		text := string(data)
		result.Text = &text
	} else {
		result.Blob = base64.StdEncoding.EncodeToString(data)
	}
	return result, nil
}
