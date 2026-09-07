package playback

import "context"

// OutputTransferHeaderV3 carries an opaque egress-created permit on the internal
// worker hop. It must never be forwarded to a client or accepted as serve authority.
const OutputTransferHeaderV3 = "X-Silo-Output-Transfer"

type ExecutorOutputTransferProviderV3 func(context.Context, string, ExecutorNamespaceV3) (string, func(), error)

type ExecutorOutputTransferGrantProviderV3 func(context.Context, string, ExecutorNamespaceV3, string) (*RuntimeGrantV3, error)
