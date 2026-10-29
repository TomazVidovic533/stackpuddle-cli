package application

import (
	"context"
	"fmt"

	"github.com/TomazVidovic533/stackpuddle/model"
)

// RemoveRequest names the declaration to remove from the YAML composition root.
type RemoveRequest struct {
	Selection Selection
	Ref       model.Ref
}

// Remove deletes one declaration from the YAML composition root and nothing physical. The realization stays
// until `destroy`; the next `up` shows it as retained.
type Remove func(ctx context.Context, req RemoveRequest) (Removed, error)

// NewRemove returns the remove use case over the declaration editor.
func NewRemove(editor DeclarationEditor) Remove {
	return func(ctx context.Context, req RemoveRequest) (Removed, error) {
		switch req.Ref.Kind() {
		case model.RefWorkload, model.RefModule, model.RefClient, model.RefApp:
			return editor.Remove(ctx, req.Selection, req.Ref)
		case model.RefEnv, model.RefResource, model.RefOperation, model.RefBinding:
		}
		return Removed{}, NewError(ClassUsage, "remove.unsupported_ref_kind",
			fmt.Sprintf("remove supports workload/<key>, module/<key>, client/<key> and app/<key>, not %s", req.Ref.Kind()))
	}
}
