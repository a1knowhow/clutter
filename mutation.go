package cuttle

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Operation struct {
	Kind         string        `json:"kind"`
	ID           ID            `json:"id"`
	Expected     Revision      `json:"expected"`
	Valid        Interval      `json:"valid"`
	Node         *Node         `json:"node,omitempty"`
	Relationship *Relationship `json:"relationship,omitempty"`
	Reason       string        `json:"reason"`
	Actor        string        `json:"actor,omitempty"`
}
type Batch struct {
	Operations []Operation `json:"operations"`
}
type state struct {
	Touched []ID      `json:"touched,omitempty"`
	Current []Version `json:"current"`
	History []Version `json:"history"`
}

func valid(i Interval) bool { return !i.From.IsZero() && (i.To.IsZero() || i.From.Before(i.To)) }
func contains(i Interval, t time.Time) bool {
	return !t.Before(i.From) && (i.To.IsZero() || t.Before(i.To))
}
func overlap(a, b Interval) bool {
	return (b.To.IsZero() || a.From.Before(b.To)) && (a.To.IsZero() || b.From.Before(a.To))
}
func interval(v Version) Interval {
	if v.Node != nil {
		return v.Node.Valid
	}
	return v.Relationship.Valid
}
func identity(v Version) ID {
	if v.Node != nil {
		return v.Node.ID
	}
	return v.Relationship.ID
}
func setInterval(v Version, i Interval) Version {
	if v.Node != nil {
		n := *v.Node
		n.Valid = i
		v.Node = &n
	} else {
		r := *v.Relationship
		r.Valid = i
		v.Relationship = &r
	}
	return v
}
func normalizeProps(p Properties) error {
	for k := range p {
		if k == "" || !textValid(k) || strings.HasPrefix(k, "_cuttle_") {
			return fmt.Errorf("%w: reserved or empty property %q", ErrInvalid, k)
		}
	}
	if e := validateJSONText(p, 0); e != nil {
		return e
	}
	data, e := json.Marshal(p)
	if e != nil {
		return fmt.Errorf("%w: properties: %v", ErrInvalid, e)
	}
	var decoded Properties
	if e = decodeJSON(data, &decoded); e != nil {
		return e
	}
	if e = validateJSONText(decoded, 0); e != nil {
		return e
	}
	if _, e = normalizeNumbers(decoded); e != nil {
		return e
	}
	return nil
}
func (s *Store) Apply(ctx context.Context, g GraphID, b Batch) (Revision, error) {
	if e := s.lock(ctx); e != nil {
		return 0, e
	}
	defer s.unlock()
	if e := s.check(ctx); e != nil {
		return 0, e
	}
	ctx, release, e := s.graphLock(ctx, g)
	if e != nil {
		return 0, e
	}
	defer release()
	commits, e := s.storage.Read(ctx, g)
	if e != nil {
		return 0, e
	}
	st, rev, e := latest(ctx, commits)
	if e != nil {
		return 0, e
	}
	if len(b.Operations) == 0 {
		return 0, fmt.Errorf("%w: empty batch", ErrInvalid)
	}
	historyStart := len(st.History)
	touched := map[ID]bool{}
	token := rev + 1
	now := s.options.Clock().UTC()
	if len(commits) > 0 && !now.After(commits[len(commits)-1].RecordedAt) {
		now = commits[len(commits)-1].RecordedAt.Add(time.Nanosecond)
	}
	for _, op := range b.Operations {
		touched[op.ID] = true
		if e = ctx.Err(); e != nil {
			return 0, canceled(e)
		}
		if op.ID == "" || !textValid(string(op.ID)) || op.Reason == "" || !textValid(op.Reason) || !textValid(op.Actor) || !valid(op.Valid) {
			return 0, fmt.Errorf("%w: identity, reason and valid interval required", ErrInvalid)
		}
		found := false
		var head Revision
		var original Version
		for _, v := range st.History {
			if identity(v) == op.ID {
				found = true
				if v.Token >= head {
					head = v.Token
					original = v
				}
			}
		}
		if op.Kind == "create" {
			if found {
				return 0, ErrExists
			}
			if op.Expected != 0 {
				return 0, ErrConflict
			}
		} else if op.Kind == "correct" || op.Kind == "retract" {
			if !found {
				return 0, ErrNotFound
			}
			if op.Expected != head {
				return 0, ErrConflict
			}
		} else {
			return 0, fmt.Errorf("%w: operation %q", ErrInvalid, op.Kind)
		}
		replacement := Version{RecordedAt: now, Token: token, Reason: op.Reason, Actor: op.Actor}
		if op.Kind != "retract" {
			if (op.Node == nil) == (op.Relationship == nil) {
				return 0, fmt.Errorf("%w: exactly one node or relationship required", ErrInvalid)
			}
			if op.Node != nil {
				n := *op.Node
				n.Identity = Identity{g, op.ID}
				n.Revision = token
				n.Valid = op.Valid
				if len(n.Labels) == 0 {
					return 0, fmt.Errorf("%w: node labels required", ErrInvalid)
				}
				labelSet := map[string]bool{}
				for _, l := range n.Labels {
					if l == "" || !textValid(l) || labelSet[l] {
						return 0, ErrInvalid
					}
					labelSet[l] = true
				}
				if e = normalizeProps(n.Properties); e != nil {
					return 0, e
				}
				if e = validateEvidence(n.Evidence, n.Properties); e != nil {
					return 0, e
				}
				replacement.Node = &n
				if found && original.Node == nil {
					return 0, ErrInvalid
				}
			} else {
				r := *op.Relationship
				r.Identity = Identity{g, op.ID}
				r.Revision = token
				r.Valid = op.Valid
				if r.From == "" || r.To == "" || r.Type == "" || !textValid(string(r.From)) || !textValid(string(r.To)) || !textValid(r.Type) {
					return 0, ErrInvalid
				}
				if e = normalizeProps(r.Properties); e != nil {
					return 0, e
				}
				if found && (original.Relationship == nil || original.Relationship.From != r.From || original.Relationship.To != r.To || original.Relationship.Type != r.Type) {
					return 0, fmt.Errorf("%w: relationship endpoints and type are immutable", ErrInvalid)
				}
				if e = validateEvidence(r.Evidence, r.Properties); e != nil {
					return 0, e
				}
				replacement.Relationship = &r
			}
		}
		next := []Version{}
		tombstones := []Version{}
		for _, v := range st.Current {
			if identity(v) != op.ID || !overlap(interval(v), op.Valid) {
				next = append(next, v)
				continue
			}
			old := interval(v)
			if op.Kind == "retract" {
				cut := Interval{From: old.From, To: old.To}
				if op.Valid.From.After(cut.From) {
					cut.From = op.Valid.From
				}
				if !op.Valid.To.IsZero() && (cut.To.IsZero() || op.Valid.To.Before(cut.To)) {
					cut.To = op.Valid.To
				}
				tomb := setInterval(v, cut)
				tomb.Retracted = true
				tomb.Token = token
				tomb.RecordedAt = now
				tomb.Reason = op.Reason
				tomb.Actor = op.Actor
				if tomb.Node != nil {
					tomb.Node.Revision = token
				} else {
					tomb.Relationship.Revision = token
				}
				tombstones = append(tombstones, tomb)
			}
			if old.From.Before(op.Valid.From) {
				next = append(next, setInterval(v, Interval{old.From, op.Valid.From}))
			}
			if !op.Valid.To.IsZero() && (old.To.IsZero() || op.Valid.To.Before(old.To)) {
				next = append(next, setInterval(v, Interval{op.Valid.To, old.To}))
			}
		}
		if op.Kind != "retract" {
			next = append(next, replacement)
			st.History = append(st.History, replacement)
		} else {
			if len(tombstones) == 0 {
				return 0, fmt.Errorf("%w: no active interval to retract", ErrNotFound)
			}
			st.History = append(st.History, tombstones...)
		}
		st.Current = next
	}
	for i, v := range st.Current {
		if !touched[identity(v)] {
			continue
		}
		if v.Node != nil {
			n := *v.Node
			n.Revision = token
			v.Node = &n
		} else {
			r := *v.Relationship
			r.Revision = token
			v.Relationship = &r
		}
		v.Token = token
		v.RecordedAt = now
		st.Current[i] = v
	}
	if e = checkEndpoints(st.Current); e != nil {
		return 0, e
	}
	delta := state{History: st.History[historyStart:]}
	for id := range touched {
		delta.Touched = append(delta.Touched, id)
	}
	for _, version := range st.Current {
		if touched[identity(version)] {
			delta.Current = append(delta.Current, version)
		}
	}
	data, e := json.Marshal(delta)
	if e != nil {
		return 0, e
	}
	if len(data) > s.options.MaxBytes*16 {
		return 0, ErrCapacity
	}
	if e = ctx.Err(); e != nil {
		return 0, canceled(e)
	}
	if e = s.storage.Append(ctx, g, rev, Commit{token, now, data}); e != nil {
		return 0, e
	}
	return token, nil
}
func checkEndpoints(vs []Version) error {
	for _, v := range vs {
		if v.Relationship == nil {
			continue
		}
		r := v.Relationship
		for _, id := range []ID{r.From, r.To} {
			parts := []Interval{}
			for _, n := range vs {
				if n.Node != nil && n.Node.ID == id && overlap(n.Node.Valid, r.Valid) {
					parts = append(parts, n.Node.Valid)
				}
			}
			cursor := r.Valid.From
			for {
				var best time.Time
				unbounded := false
				for _, p := range parts {
					if !cursor.Before(p.From) && (p.To.IsZero() || cursor.Before(p.To)) {
						if p.To.IsZero() {
							unbounded = true
							break
						}
						if p.To.After(best) {
							best = p.To
						}
					}
				}
				if unbounded {
					break
				}
				if best.IsZero() {
					return fmt.Errorf("%w: relationship %s endpoint %s", ErrStranded, r.ID, id)
				}
				if !r.Valid.To.IsZero() && !best.Before(r.Valid.To) {
					break
				}
				cursor = best
			}
		}
	}
	return nil
}
