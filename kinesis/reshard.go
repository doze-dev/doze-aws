package kinesis

// Resharding: SplitShard, MergeShards and UpdateShardCount.
//
// Resharding is the one part of Kinesis that is genuinely subtle, and the
// subtlety is ordering. A shard is never mutated in place: it is CLOSED at the
// current end of the stream and children are opened to cover its hash range
// from there on. Consumers drain the parent to its ending sequence, learn the
// children from the null NextShardIterator plus ChildShards, and only then
// start reading them. That is what keeps records for one partition key in order
// across a reshard — so it is modelled properly rather than approximated by
// rewriting hash ranges.

import (
	"math/big"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// closeShard marks a shard closed at the current end of the stream.
func closeShard(st *Stream, sh *Shard) {
	sh.Closed = true
	if st.NextSeq > 1 {
		sh.EndSeq = st.NextSeq - 1
	}
}

// openChild appends a new open shard covering [lo, hi] with the given parents.
func openChild(st *Stream, lo, hi *big.Int, parent, adjacent string) Shard {
	sh := Shard{
		ID:         shardID(st.NextShardNum),
		StartHash:  lo.String(),
		EndHash:    hi.String(),
		ParentID:   parent,
		AdjacentID: adjacent,
		StartSeq:   st.NextSeq,
	}
	st.NextShardNum++
	st.Shards = append(st.Shards, sh)
	return sh
}

func hSplitShard(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	target := awsjson.Str(p, "ShardToSplit")
	if target == "" {
		return nil, errValidation("ShardToSplit is required")
	}
	newStartStr := awsjson.Str(p, "NewStartingHashKey")
	newStart, ok := new(big.Int).SetString(newStartStr, 10)
	if !ok {
		return nil, errInvalid("NewStartingHashKey must be a decimal integer")
	}

	_, err := s.store.Update(stream, func(st *Stream) error {
		sh, found := st.shard(target)
		if !found {
			return errNoShard(target, stream)
		}
		if sh.Closed {
			return errInvalid("shard %s is already closed", target)
		}
		lo, _ := new(big.Int).SetString(sh.StartHash, 10)
		hi, _ := new(big.Int).SetString(sh.EndHash, 10)
		// The split point must fall strictly inside the parent: splitting at
		// the parent's own start would leave an empty child.
		if newStart.Cmp(lo) <= 0 || newStart.Cmp(hi) > 0 {
			return errInvalid("NewStartingHashKey %s must be within (%s, %s] for shard %s",
				newStart, lo, hi, target)
		}
		closeShard(st, sh)
		openChild(st, lo, new(big.Int).Sub(newStart, big.NewInt(1)), target, "")
		openChild(st, newStart, hi, target, "")
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

func hMergeShards(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	a := awsjson.Str(p, "ShardToMerge")
	b := awsjson.Str(p, "AdjacentShardToMerge")
	if a == "" || b == "" {
		return nil, errValidation("both ShardToMerge and AdjacentShardToMerge are required")
	}
	if a == b {
		return nil, errInvalid("a shard cannot be merged with itself")
	}

	_, err := s.store.Update(stream, func(st *Stream) error {
		left, ok1 := st.shard(a)
		right, ok2 := st.shard(b)
		if !ok1 {
			return errNoShard(a, stream)
		}
		if !ok2 {
			return errNoShard(b, stream)
		}
		if left.Closed || right.Closed {
			return errInvalid("both shards must be open to merge")
		}
		lLo, _ := new(big.Int).SetString(left.StartHash, 10)
		lHi, _ := new(big.Int).SetString(left.EndHash, 10)
		rLo, _ := new(big.Int).SetString(right.StartHash, 10)
		rHi, _ := new(big.Int).SetString(right.EndHash, 10)
		// Normalise so left is the lower range, then require true adjacency:
		// merging a gap would silently drop part of the hash space.
		if lLo.Cmp(rLo) > 0 {
			left, right = right, left
			lLo, lHi, rLo, rHi = rLo, rHi, lLo, lHi
		}
		if new(big.Int).Add(lHi, big.NewInt(1)).Cmp(rLo) != 0 {
			return errInvalid("shards %s and %s are not adjacent", a, b)
		}
		closeShard(st, left)
		closeShard(st, right)
		openChild(st, lLo, rHi, left.ID, right.ID)
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

func hUpdateShardCount(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	target := awsjson.Int(p, "TargetShardCount", 0)
	if target < 1 {
		return nil, errInvalid("TargetShardCount must be at least 1")
	}
	if st := awsjson.Str(p, "ScalingType"); st != "" && st != "UNIFORM_SCALING" {
		return nil, errValidation("ScalingType must be UNIFORM_SCALING")
	}

	var current int
	out, err := s.store.Update(stream, func(st *Stream) error {
		if st.Mode == modeOnDemand {
			return errValidation("shard count is managed automatically for ON_DEMAND streams")
		}
		open := st.OpenShards()
		current = len(open)
		if current == target {
			return nil
		}
		// Close every open shard and re-tile the hash space. Each child names
		// the parent that owned its starting hash, so lineage still resolves —
		// an approximation of AWS's pairwise split/merge plan, but one that
		// preserves the drain-parent-then-children contract consumers rely on.
		parentAt := func(h *big.Int) string {
			for i := range open {
				lo, _ := new(big.Int).SetString(open[i].StartHash, 10)
				hi, _ := new(big.Int).SetString(open[i].EndHash, 10)
				if h.Cmp(lo) >= 0 && h.Cmp(hi) <= 0 {
					return open[i].ID
				}
			}
			return ""
		}
		for i := range st.Shards {
			if !st.Shards[i].Closed {
				closeShard(st, &st.Shards[i])
			}
		}
		span := new(big.Int).Div(new(big.Int).Add(maxHash, big.NewInt(1)), big.NewInt(int64(target)))
		lo := big.NewInt(0)
		for i := 0; i < target; i++ {
			hi := new(big.Int).Sub(new(big.Int).Add(lo, span), big.NewInt(1))
			if i == target-1 {
				hi = new(big.Int).Set(maxHash)
			}
			openChild(st, lo, hi, parentAt(lo), "")
			lo = new(big.Int).Add(hi, big.NewInt(1))
		}
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"StreamName":        out.Name,
		"CurrentShardCount": current,
		"TargetShardCount":  target,
		"StreamARN":         streamARN(out.Name),
	}, nil
}
