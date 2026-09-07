package playback

import "context"

// AuxiliaryTransferHeaderV3 is an internal, egress-created permit locator. It
// never authorizes final serving and is never forwarded to the viewer.
const AuxiliaryTransferHeaderV3 = "X-Silo-Auxiliary-Transfer"

// Auxiliary transfer is API catalog/S3 production for the selected proxy, not
// execution-node output and not authority to serve a client directly.
const AttemptGrantAuxiliaryV3 AttemptGrantPurposeV3 = "auxiliary_transfer"

type ExecutorAuxiliaryTransferProviderV3 func(context.Context, string, ExecutorNamespaceV3) (string, func(), error)
type ExecutorAuxiliaryGrantProviderV3 func(context.Context, string, ExecutorNamespaceV3, string) (*RuntimeGrantV3, error)

// AuxiliaryRecipeV3 keeps the original requested file beside the immutable
// executor recipe. It is read from the committed attempt, never a query value.
type AuxiliaryRecipeV3 struct {
	Card                 *RecipeCard
	RequestedMediaFileID int
}

type AuxiliaryRecipeResolverV3 func(context.Context, string, ExecutorNamespaceV3) (AuxiliaryRecipeV3, error)
