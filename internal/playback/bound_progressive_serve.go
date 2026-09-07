package playback

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// ServeHTTP reads only an actual retained producer. Grants cover every byte and
// cancellation interrupts readers even while the producer has not emitted more.
func (r *BoundProgressiveRegistryV3) ServeHTTP(w http.ResponseWriter, request *http.Request, transport string, ns ExecutorNamespaceV3, acquire ExecutorGrantProviderV3, purpose AttemptGrantPurposeV3) error {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return errors.New("progressive output requires GET or HEAD")
	}
	// This progressive representation ignores Range and returns HTTP 200 from
	// byte zero. Seeking is an explicit successor intent, never GET-time remux.
	e, err := r.lookup(transport, ns)
	if err != nil {
		return err
	}
	e.mu.Lock()
	ready := e.readyOK
	e.mu.Unlock()
	if !ready || e.ctx.Err() != nil {
		return ErrBoundProgressiveMissingV3
	}
	writer, req, cleanup, err := GuardExecutorOutputV3(w, request, acquire, transport, &ns, purpose)
	if err != nil {
		return err
	}
	defer cleanup()
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	file, err := os.Open(filepath.Join(e.prepared.output, "output.mp4"))
	if err != nil {
		return ErrBoundProgressiveMissingV3
	}
	defer file.Close()
	writer.Header().Set("Content-Type", boundProgressiveContentTypeV3(e.prepared.card.AudioOnly))
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Accept-Ranges", "none")
	if request.Method == http.MethodHead {
		writer.WriteHeader(http.StatusOK)
		return nil
	}
	buffer := make([]byte, 32<<10)
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.mu.Lock()
		size, finished, changed := e.size, e.finished, e.changed
		e.mu.Unlock()
		if offset < size {
			n, readErr := file.ReadAt(buffer[:min(int64(len(buffer)), size-offset)], offset)
			if n > 0 {
				if _, err := writer.Write(buffer[:n]); err != nil {
					return err
				}
				offset += int64(n)
			}
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return readErr
			}
			continue
		}
		if finished {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// ServeRemoteBoundProgressiveV3 holds the final serving grant and the existing
// output-transfer permit until the selected worker response has finished. There
// is exactly one GET, no redirect following, retries or local reconstruction.
func ServeRemoteBoundProgressiveV3(w http.ResponseWriter, r *http.Request, card RecipeCard, endpoint, bearer string, acquire ExecutorGrantProviderV3, transfer ExecutorOutputTransferProviderV3) error {
	if err := ValidateBoundProgressiveRecipeV3(card); err != nil {
		return err
	}
	if transfer == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return errors.New("progressive egress unavailable")
	}
	writer, req, cleanup, err := GuardExecutorResponseV3(w, r, acquire, card.TranscodeTransportID, card.Executor)
	if err != nil {
		return err
	}
	defer cleanup()
	permit, release, err := transfer(req.Context(), card.TranscodeTransportID, *card.Executor)
	if release != nil {
		defer release()
	}
	if err != nil {
		return err
	}
	if permit == "" || release == nil {
		return errors.New("progressive transfer permit missing")
	}
	outgoing, err := http.NewRequestWithContext(req.Context(), r.Method, endpoint, nil)
	if err != nil {
		return errors.New("invalid progressive worker endpoint")
	}
	outgoing.Header.Set("Authorization", "Bearer "+bearer)
	outgoing.Header.Set(OutputTransferHeaderV3, permit)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(outgoing)
	if err != nil {
		return errors.New("progressive worker output unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("progressive worker output refused")
	}
	writer.Header().Set("Content-Type", boundProgressiveContentTypeV3(card.AudioOnly))
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Accept-Ranges", "none")
	writer.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(writer, response.Body)
	return err
}

func boundProgressiveContentTypeV3(audioOnly bool) string {
	if audioOnly {
		return RemuxContentType(true)
	}
	return containerMIME("mp4")
}
