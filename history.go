package cuttle

import "context"

func (s *Store) History(ctx context.Context, g GraphID, id ID) ([]Version, error) {
	if e := s.lock(ctx); e != nil {
		return nil, e
	}
	defer s.unlock()
	if e := s.check(ctx); e != nil {
		return nil, e
	}
	ctx, release, e := s.graphLock(ctx, g)
	if e != nil {
		return nil, e
	}
	defer release()
	cs, e := s.storage.Read(ctx, g)
	if e != nil {
		return nil, e
	}
	st, _, e := latest(ctx, cs)
	if e != nil {
		return nil, e
	}
	out := []Version{}
	for _, v := range st.History {
		if id == "" || identity(v) == id {
			out = append(out, v)
		}
	}
	if e := ctx.Err(); e != nil {
		return nil, canceled(e)
	}
	return out, nil
}
