package mediasource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	storagev1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/bloem/plugin/v1"
	"google.golang.org/grpc"
)

type pluginSource struct {
	client storagev1.StorageProviderClient
}

func NewPluginSource(client storagev1.StorageProviderClient) Source {
	return &pluginSource{client: client}
}

func (s *pluginSource) Stat(ctx context.Context, ref Ref) (Info, error) {
	if err := validateRef(ref); err != nil {
		return Info{}, err
	}
	if s.client == nil {
		return Info{}, fmt.Errorf("storage client is unavailable")
	}
	response, err := s.client.Stat(ctx, &storagev1.StatRequest{SourceId: ref.SourceID, EntryId: ref.EntryID, ExpectedRevision: ref.Revision}, grpc.MaxCallRecvMsgSize(80<<10))
	if err != nil {
		return Info{}, err
	}
	entry := response.GetEntry()
	if entry == nil || entry.GetId() != ref.EntryID || entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE || entry.GetRevision() != ref.Revision {
		return Info{}, fmt.Errorf("storage Stat did not return the pinned file")
	}
	info := Info{Name: entry.GetName(), LogicalPath: entry.GetLogicalPath(), Revision: entry.GetRevision(), Size: entry.GetSize(), ModifiedAt: time.Unix(0, entry.GetModifiedUnixNano())}
	if err := validateInfo(info); err != nil {
		return Info{}, err
	}
	return info, nil
}

func (s *pluginSource) ReadRange(ctx context.Context, ref Ref, offset, length int64, w io.Writer) error {
	if err := validateRef(ref); err != nil {
		return err
	}
	if s.client == nil || w == nil {
		return fmt.Errorf("storage range reader is unavailable")
	}
	if offset < 0 || length < 0 || length > maxRange || offset > 1<<63-1-length {
		return fmt.Errorf("invalid storage range")
	}
	if length == 0 {
		return ctx.Err()
	}
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := s.client.Read(readCtx, &storagev1.ReadRequest{SourceId: ref.SourceID, EntryId: ref.EntryID, ExpectedRevision: ref.Revision, Offset: offset, Length: length}, grpc.MaxCallRecvMsgSize(maxChunk+1024))
	if err != nil {
		return err
	}
	next := offset
	end := offset + length
	terminal := false
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if !terminal || next != end {
				return fmt.Errorf("storage stream incomplete: %w", io.ErrUnexpectedEOF)
			}
			return nil
		}
		if err != nil {
			return err
		}
		if terminal || chunk == nil || chunk.GetOffset() != next || len(chunk.GetData()) > maxChunk || int64(len(chunk.GetData())) > end-next || len(chunk.GetData()) == 0 {
			return fmt.Errorf("invalid storage stream chunk")
		}
		if chunk.GetEof() && next+int64(len(chunk.GetData())) != end {
			return fmt.Errorf("storage stream truncated: %w", io.ErrUnexpectedEOF)
		}
		n, err := w.Write(chunk.GetData())
		if err != nil {
			return err
		}
		if n != len(chunk.GetData()) {
			return io.ErrShortWrite
		}
		next += int64(n)
		terminal = chunk.GetEof()
		// Read the final status too: data alone does not establish a pinned read.
	}
}
