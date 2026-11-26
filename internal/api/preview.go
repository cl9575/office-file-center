package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"lanfile/internal/apperr"
	"lanfile/internal/audit"
	"lanfile/internal/model"
)

const textPreviewLimit = 1 << 20

type previewFormat struct {
	kind        string
	contentType string
	text        bool
}

var previewFormats = map[string]previewFormat{
	".jpg":      {kind: "image", contentType: "image/jpeg"},
	".jpeg":     {kind: "image", contentType: "image/jpeg"},
	".png":      {kind: "image", contentType: "image/png"},
	".webp":     {kind: "image", contentType: "image/webp"},
	".gif":      {kind: "image", contentType: "image/gif"},
	".bmp":      {kind: "image", contentType: "image/bmp"},
	".avif":     {kind: "image", contentType: "image/avif"},
	".ico":      {kind: "image", contentType: "image/x-icon"},
	".pdf":      {kind: "pdf", contentType: "application/pdf"},
	".txt":      {kind: "text", contentType: "text/plain; charset=utf-8", text: true},
	".log":      {kind: "text", contentType: "text/plain; charset=utf-8", text: true},
	".ini":      {kind: "text", contentType: "text/plain; charset=utf-8", text: true},
	".csv":      {kind: "text", contentType: "text/plain; charset=utf-8", text: true},
	".json":     {kind: "text", contentType: "text/plain; charset=utf-8", text: true},
	".xml":      {kind: "text", contentType: "text/plain; charset=utf-8", text: true},
	".md":       {kind: "text", contentType: "text/plain; charset=utf-8", text: true},
	".markdown": {kind: "text", contentType: "text/plain; charset=utf-8", text: true},
	".mp3":      {kind: "audio", contentType: "audio/mpeg"},
	".wav":      {kind: "audio", contentType: "audio/wav"},
	".m4a":      {kind: "audio", contentType: "audio/mp4"},
	".ogg":      {kind: "audio", contentType: "audio/ogg"},
	".oga":      {kind: "audio", contentType: "audio/ogg"},
	".mp4":      {kind: "video", contentType: "video/mp4"},
	".m4v":      {kind: "video", contentType: "video/mp4"},
	".webm":     {kind: "video", contentType: "video/webm"},
	".ogv":      {kind: "video", contentType: "video/ogg"},
}

func previewFormatFor(name string) (previewFormat, bool) {
	format, ok := previewFormats[strings.ToLower(filepath.Ext(name))]
	return format, ok
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request, user model.User) {
	node, version, err := s.Files.ResolveVersion(r.PathValue("id"), "", actorOf(user, r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	format, ok := previewFormatFor(node.Name)
	if !ok {
		s.writeErr(w, r, apperr.New(http.StatusUnsupportedMediaType, "PREVIEW_UNSUPPORTED",
			"该文件类型不支持浏览器预览，请下载原文件"))
		return
	}
	f, err := s.Files.Store.Open(version.ObjectID)
	if err != nil {
		_ = s.Audit.Log(audit.Event{
			ActorID: user.ID, ActorName: user.DisplayName, Action: "preview", TargetType: "file",
			TargetID: node.ID, TargetName: node.Name, TargetPath: node.Path,
			Detail: fmt.Sprintf("V%d 内容缺失", version.VersionNo), Result: audit.ResultFailure,
			IP: requestIP(r), Secure: node.Space == model.SpaceSecure,
		})
		s.writeErr(w, r, apperr.Internal("文件内容缺失，请联系管理员检查存储或备份"))
		return
	}
	defer f.Close()

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("Content-Disposition", inlineHeader(node.Name))
	w.Header().Set("Content-Type", format.contentType)

	cw := &countingWriter{ResponseWriter: w}
	if format.text {
		content, truncated, err := readTextPreview(f)
		if err != nil {
			_ = s.Audit.Log(audit.Event{
				ActorID: user.ID, ActorName: user.DisplayName, Action: "preview", TargetType: "file",
				TargetID: node.ID, TargetName: node.Name, TargetPath: node.Path,
				Detail: "读取文本预览失败：" + err.Error(), Result: audit.ResultFailure,
				IP: requestIP(r), Secure: node.Space == model.SpaceSecure,
			})
			s.writeErr(w, r, apperr.Internal("读取预览内容失败"))
			return
		}
		if truncated {
			w.Header().Set("X-Preview-Truncated", "1")
		}
		http.ServeContent(cw, r, node.Name, time.Time{}, bytes.NewReader(content))
	} else {
		http.ServeContent(cw, r, node.Name, time.Time{}, f)
	}

	if shouldAuditPreview(r) {
		detail := fmt.Sprintf("预览 V%d，类型 %s，实际发送 %d 字节", version.VersionNo, format.contentType, cw.n)
		if format.text && r.Header.Get("Range") == "" {
			detail = fmt.Sprintf("预览 V%d，文本，实际发送 %d 字节", version.VersionNo, cw.n)
		}
		_ = s.Audit.Log(audit.Event{
			ActorID: user.ID, ActorName: user.DisplayName, Action: "preview", TargetType: "file",
			TargetID: node.ID, TargetName: node.Name, TargetPath: node.Path,
			Detail: detail, Result: audit.ResultSuccess, IP: requestIP(r),
			Secure: node.Space == model.SpaceSecure,
		})
	}
}

func shouldAuditPreview(r *http.Request) bool {
	value := r.Header.Get("Range")
	return value == "" || strings.HasPrefix(value, "bytes=0-")
}

func readTextPreview(f *os.File) ([]byte, bool, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(io.LimitReader(f, textPreviewLimit))
	if err != nil {
		return nil, false, err
	}
	return decodePreviewText(data), info.Size() > textPreviewLimit, nil
}

func decodePreviewText(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if complete, ok := completeUTF8Prefix(data); ok {
		return complete
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
	if err != nil {
		return []byte(strings.ToValidUTF8(string(data), "\uFFFD"))
	}
	return decoded
}

func completeUTF8Prefix(data []byte) ([]byte, bool) {
	if utf8.Valid(data) {
		return data, true
	}
	for trim := 1; trim <= utf8.UTFMax-1 && trim < len(data); trim++ {
		prefix := data[:len(data)-trim]
		if !utf8.Valid(prefix) {
			continue
		}
		tail := data[len(prefix):]
		if incompleteUTF8Sequence(tail) {
			return prefix, true
		}
	}
	return nil, false
}

func incompleteUTF8Sequence(tail []byte) bool {
	if len(tail) == 0 || len(tail) >= utf8.UTFMax {
		return false
	}
	need := 0
	switch first := tail[0]; {
	case first >= 0xC2 && first <= 0xDF:
		need = 2
	case first >= 0xE0 && first <= 0xEF:
		need = 3
	case first >= 0xF0 && first <= 0xF4:
		need = 4
	default:
		return false
	}
	if len(tail) >= need {
		return false
	}
	for _, b := range tail[1:] {
		if b < 0x80 || b > 0xBF {
			return false
		}
	}
	return true
}
