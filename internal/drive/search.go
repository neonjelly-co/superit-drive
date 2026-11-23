package drive

import (
	"context"

	"github.com/neonjelly-co/superit-drive/internal/index"
)

// Search finds live items whose names match what is searched for, in scope only (index.Search): the best matches
// first, at most limit. It reads the index alone, not the storage: what a listing has not seen yet is not found. It
// holds no place's lock. index.ErrSearchBusy when too many searches are under way, index.ErrSearchTooLong when this one
// was ended.
func (d *Drive) Search(ctx context.Context, scope index.Scope, search string, limit int) ([]index.Item, error) {
	return d.index.Search(ctx, scope, search, limit)
}

// FindMyFiles finds the member's "My files" without making it, as MyFiles does the first time: index.ErrNoPlace when
// they have none yet.
func (d *Drive) FindMyFiles(ctx context.Context, userID string) (index.Place, error) {
	return d.index.MyFiles(ctx, userID)
}
