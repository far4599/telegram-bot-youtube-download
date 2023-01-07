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
	ErrInvalidURL = fmt.Errorf("invalid url")
	ErrNotFound   = fmt.Errorf("not found")
)

const cacheDir = "/tmp/yt-dlp"

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

func (s *VideoService) GetVideoInfo(ctx context.Context, url string) (*models.VideoInfo, error) {
	out, err := readAll(s.runWithRetry(ctx, url, true))
	if err != nil {
		if !errors.Is(err, retry.Error{}) {
			return nil, errors.Wrapf(ErrInvalidURL, "failed to get info for the provided url: '%v'", err)
		}

		return nil, err
	}

	json, err := new(fastjson.Parser).ParseBytes(out)
	if err != nil {
		return nil, err
	}

	return &models.VideoInfo{
		URL:      url,
		Title:    string(json.GetStringBytes("title")),
		ThumbURL: string(json.GetStringBytes("thumbnail")),
		Duration: json.GetInt("duration"),
		Vertical: isVertical(json),
		Youtube:  isYoutube(json),
	}, nil
}

func (s *VideoService) GetVideoOptions(ctx context.Context, videoInfo *models.VideoInfo) ([]*models.VideoOption, error) {
	result := make([]*models.VideoOption, 0, 4)

	dim := "height"
	if videoInfo.Vertical {
		dim = "width"
	}

	mu := sync.Mutex{}
	errGroup, errCtx := errgroup.WithContext(ctx)

	if videoInfo.Youtube {
		errGroup.Go(func() error {
			opt, err := s.getAudioOption(errCtx, videoInfo.URL)
			if err != nil {
				return nil
			}

			opt.VideoInfo = *videoInfo
			s.saveToCache(opt)

			mu.Lock()
			defer mu.Unlock()

			result = append(result, opt)

			return nil
		})
	}

	sizes := []string{"300", "600", "1000"}
	for _, size := range sizes {
		size := size

		errGroup.Go(func() error {
			opt, err := s.getVideoOption(errCtx, videoInfo.URL, dim, size)
			if err != nil {
				return nil
			}

			opt.VideoInfo = *videoInfo
			s.saveToCache(opt)

			mu.Lock()
			defer mu.Unlock()

			result = append(result, opt)

			return nil
		})
	}

	errGroup.Wait()

	return result, nil
}

func (s *VideoService) DownloadVideo(ctx context.Context, id string) (videoOption *models.VideoOption, err error) {
	var ok bool

	videoOption, ok = s.getFromCache(id)
	if !ok {
		return nil, ErrNotFound
	}

	fileName := id + ".mp4"
	if videoOption.Audio {
		fileName = id + ".mp3"
	}

	filePath := os.TempDir()

	args := []string{
		"-o", fileName,
		"-P", filePath,
		"-f", videoOption.FormatID,
		"--no-progress",
		// "--force-overwrites",
	}

	_, err = readAll(s.runWithRetry(ctx, videoOption.VideoInfo.URL, false, args...))
	if err != nil {
		return nil, err
	}

	videoOption.Path = path.Join(filePath, fileName)

	return videoOption, nil
}

func (s *VideoService) runWithRetry(ctx context.Context, url string, isJson bool, args ...string) (result io.ReadCloser, err error) {
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

func runYtDlp(ctx context.Context, url string, isJson bool, args ...string) (io.ReadCloser, error) {
	defaultArgs := []string{
		"--ignore-errors",
		"--no-call-home",
		"--cache-dir", cacheDir,
		// provide URL via stdin for security, youtube-dl has some run command args
		"--batch-file", "-",
	}

	if isJson {
		defaultArgs = append(defaultArgs, "-j")
	}

	args = append(defaultArgs, args...)

	log.Logger.Infow("yt-dlp arguments", "args", args)

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

	go func() {
		const errorPrefix = "ERROR: "
		stderrLineScanner := bufio.NewScanner(errOut)
		for stderrLineScanner.Scan() {
			line := stderrLineScanner.Text()
			if strings.HasPrefix(line, errorPrefix) {
				log.Logger.Errorw("yt-dlp returned error", "error", line[len(errorPrefix):])
			}
		}
	}()

	if err = cmd.Start(); err != nil {
		return nil, err
	}

	go cmd.Wait()

	return out, nil
}

func readAll(r io.ReadCloser, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	defer r.Close()

	return io.ReadAll(r)
}

func (s *VideoService) getAudioOption(ctx context.Context, url string) (*models.VideoOption, error) {
	out, err := readAll(s.runWithRetry(ctx, url, true, "-f", "bestaudio[ext=m4a]", "--skip-download"))
	if err != nil {
		return nil, err
	}

	json, err := new(fastjson.Parser).ParseBytes(out)
	if err != nil {
		return nil, err
	}

	return &models.VideoOption{
		FormatID: getFormatID(json),
		Label:    "only audio",
		Size:     getFilesize(json),
		Audio:    true,
	}, nil
}

func (s *VideoService) getVideoOption(ctx context.Context, url, dim, size string) (*models.VideoOption, error) {
	format := fmt.Sprintf("worst[%s>%s]", dim, size)
	out, err := readAll(s.runWithRetry(ctx, url, true, "-f", format, "--skip-download"))
	if err != nil {
		return nil, err
	}

	json, err := new(fastjson.Parser).ParseBytes(out)
	if err != nil {
		return nil, err
	}

	return &models.VideoOption{
		FormatID: getFormatID(json),
		Label:    getLabel(json, dim),
		Size:     getFilesize(json),
	}, nil
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

func getLabel(v *fastjson.Value, dim string) string {
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
