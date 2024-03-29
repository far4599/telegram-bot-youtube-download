package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/avast/retry-go/v4"
	"github.com/far4599/telegram-bot-youtube-download/internal/models"
	"github.com/far4599/telegram-bot-youtube-download/internal/pkg/log"
	"github.com/far4599/telegram-bot-youtube-download/internal/repository"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/valyala/fastjson"
	"golang.org/x/sync/errgroup"
)

var (
	ErrInvalidURL    = fmt.Errorf("invalid url")
	ErrNotFound      = fmt.Errorf("not found")
	ErrVideoNotFound = fmt.Errorf("video not found")

	preferedAudioExt = []string{"m4a", "mp3", "webm"}
	preferedVideoExt = []string{"mp4", "webm", "3gp"}
)

const (
	cacheDir = "/tmp/yt-dlp"
	tmpDir   = "/tmp"
)

type VideoService struct {
	maxRetry uint
	repo     *repository.InMemRepository
}

func NewVideoService(maxRetry uint, repo *repository.InMemRepository) (*VideoService, error) {
	return &VideoService{
		maxRetry: maxRetry,
		repo:     repo,
	}, nil
}

func (s *VideoService) GetVideoInfo(ctx context.Context, url string) (*models.VideoInfo, *fastjson.Value, error) {
	out, err := readAll(s.runWithRetry(ctx, url, true, "--no-download"))
	if err != nil {
		if !errors.Is(err, new(retry.Error)) {
			return nil, nil, ErrInvalidURL
		}

		return nil, nil, err
	}

	if len(out) == 0 {
		return nil, nil, ErrVideoNotFound
	}

	json, err := new(fastjson.Parser).ParseBytes(out)
	if err != nil {
		return nil, nil, ErrVideoNotFound
	}

	return &models.VideoInfo{
		URL:      url,
		Title:    string(json.GetStringBytes("title")),
		ThumbURL: string(json.GetStringBytes("thumbnail")),
		Duration: json.GetInt("duration"),
		Vertical: isVertical(json),
		Youtube:  isYoutube(json),
	}, json, nil
}

func (s *VideoService) GetVideoOptions(videoInfo *models.VideoInfo, json *fastjson.Value) ([]*models.VideoOption, error) {
	result := make([]*models.VideoOption, 0, 4)

	if videoInfo.Youtube {
		opt, err := s.getVideoOption(json, -1)
		if err == nil {
			opt.VideoInfo = *videoInfo
			s.saveToCache(opt)

			result = append(result, opt)
		}
	}

	sizes := []int{300, 600, 1000}
	for _, size := range sizes {
		opt, err := s.getVideoOption(json, size)
		if err == nil {
			opt.VideoInfo = *videoInfo
			s.saveToCache(opt)

			result = append(result, opt)
		}
	}

	return result, nil
}

func (s *VideoService) getVideoOption(json *fastjson.Value, size int) (*models.VideoOption, error) {
	extFilter := preferedVideoExt
	var audio bool
	if size == -1 {
		extFilter = preferedAudioExt
		audio = true
	}

	found := make(map[string][]*fastjson.Value)

	formats := json.GetArray("formats")
	for _, format := range formats {
		ext := string(format.GetStringBytes("ext"))

		found[ext] = append(found[ext], format)
	}

	vertical := isVertical(json)

	var selected *fastjson.Value
	for _, ext := range extFilter {
		if selected != nil {
			break
		}

		for _, format := range found[ext] {
			if audio {
				selected = format
				continue
			}

			withAudio := format.GetInt("abr") > 0 || format.GetInt("asr") > 0 || string(format.GetStringBytes("acodec")) != "none" || string(format.GetStringBytes("audio_ext")) != "none"

			dimSize := strings.Split(string(format.GetStringBytes("resolution")), "x")
			if len(dimSize) != 2 {
				continue
			}

			dim := 1
			if vertical {
				dim = 0
			}

			i, err := strconv.Atoi(dimSize[dim])
			if err != nil {
				return nil, err
			}

			if i >= size {
				if withAudio {
					selected = format
					break
				}
			}
		}
	}

	if selected == nil {
		return nil, ErrNotFound
	}

	return &models.VideoOption{
		FormatID: getFormatID(selected),
		Label:    getLabel(selected, audio, vertical),
		Size:     getFilesize(selected),
		Audio:    audio,
	}, nil
}

func (s *VideoService) DownloadVideo(ctx context.Context, videoOption *models.VideoOption) (string, error) {
	args := []string{
		"-o", "-",
		"-f", videoOption.FormatID,
		"--no-progress",
	}

	resp, err := s.runWithRetry(ctx, videoOption.VideoInfo.URL, false, args...)
	if err != nil {
		return "", err
	}
	defer resp.Close()

	fileName := videoOption.ID + ".mp4"
	if videoOption.Audio {
		fileName = videoOption.ID + ".mp3"
	}
	filePath := path.Join(tmpDir, fileName)

	errGroup, errCtx := errgroup.WithContext(ctx)

	errGroup.Go(func() error {
		select {
		case <-errCtx.Done():
			return nil
		case <-resp.closeCh:
			return nil
		case dlpErr, ok := <-resp.errCh:
			if !ok {
				return nil
			}
			return dlpErr
		}
	})

	errGroup.Go(func() error {
		f, errG := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0744)
		if errG != nil {
			return errG
		}
		defer f.Close()

		_, errG = io.Copy(f, resp.out)
		if errG != nil {
			return errG
		}

		resp.Close()

		return nil
	})

	err = errGroup.Wait()
	if err != nil {
		defer os.Remove(filePath)

		return "", err
	}

	return filePath, nil
}

func (s *VideoService) runWithRetry(ctx context.Context, url string, isJson bool, args ...string) (result *dlpResponse, err error) {
	err = retry.Do(
		func() error {
			res, errR := runYtDlp(ctx, url, isJson, args...)
			if errR != nil {
				return errR
			}

			result = res

			return nil
		},
		retry.Context(ctx),
		retry.Attempts(s.maxRetry),
	)

	return
}

func runYtDlp(ctx context.Context, url string, isJson bool, args ...string) (*dlpResponse, error) {
	defaultArgs := []string{
		"-q", "-v",
		"--ignore-errors",
		"--no-call-home",
		"--geo-bypass",
		"--cache-dir", cacheDir,
		// provide URL via stdin for security, youtube-dl has some run command args
		"--batch-file", "-",
	}

	if isJson {
		defaultArgs = append(defaultArgs, "-j")
	}

	args = append(defaultArgs, args...)

	log.Logger.Infow("yt-dlp arguments", "args", args, "url", url)

	cmd := exec.CommandContext(
		ctx,
		"yt-dlp",
		args...,
	)

	cmd.Stdin = bytes.NewBufferString(url + "\n")

	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	errOut, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	errCh := make(chan error, 10)

	go func() {
		const errorPrefix = "ERROR: "
		stderrLineScanner := bufio.NewScanner(errOut)
		for stderrLineScanner.Scan() {
			line := stderrLineScanner.Text()
			if strings.HasPrefix(line, errorPrefix) {
				log.Logger.Errorw("yt-dlp returned error", "error", line)
				errCh <- dlpError(line)
			} else {
				log.Logger.Debug(line)
			}
		}
	}()

	if err = cmd.Start(); err != nil {
		return nil, err
	}

	closeCh := make(chan struct{})

	go func() {
		<-closeCh

		cmd.Wait()
	}()

	return &dlpResponse{
		out:     out,
		errCh:   errCh,
		closeCh: closeCh,
	}, nil
}

func readAll(resp *dlpResponse, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	return io.ReadAll(resp.out)
}

func isVertical(v *fastjson.Value) bool {
	w, err := v.Get("width").Int64()
	if err != nil {
		panic(err)
	}

	h, err := v.Get("height").Int64()
	if err != nil {
		panic(err)
	}

	return h > w
}

func isYoutube(v *fastjson.Value) bool {
	extractor := v.Get("extractor").String()
	return strings.Contains(extractor, "youtube")
}

func getFormatID(v *fastjson.Value) string {
	return strings.Trim(v.Get("format_id").String(), `"`)
}

func getLabel(v *fastjson.Value, audio, vertical bool) string {
	if audio {
		return "only audio"
	}

	dim := "height"
	if vertical {
		dim = "width"
	}

	return "p" + v.Get(dim).String()
}

func getFilesize(v *fastjson.Value) uint64 {
	size := int64(0)
	if v.Get("filesize") != nil {
		size, _ = v.Get("filesize").Int64()
	}
	if v.Get("filesize_approx") != nil {
		size, _ = v.Get("filesize_approx").Int64()
	}

	return uint64(size)
}

func (s *VideoService) saveToCache(opt *models.VideoOption) {
	opt.ID = uuid.New().String()

	s.repo.Add(opt.ID, opt)
}

func (s *VideoService) getFromCache(id string) (*models.VideoOption, bool) {
	cachedOption, ok := s.repo.Get(id)
	if !ok {
		return nil, false
	}

	videoOption, ok := cachedOption.(*models.VideoOption)
	if !ok {
		defer s.repo.Remove(id)
		return nil, false
	}

	return videoOption, true
}

type dlpResponse struct {
	out     io.ReadCloser
	errCh   chan error
	closeCh chan struct{}

	closeMu sync.Mutex
	closed  bool
}

func (r *dlpResponse) Close() {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()

	if r.closed {
		return
	}

	defer close(r.closeCh)
	defer r.out.Close()

	r.closed = true
}

type dlpError string

func (e dlpError) Error() string {
	return string(e)
}
