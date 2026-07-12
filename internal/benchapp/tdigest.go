// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"github.com/influxdata/tdigest"
	"github.com/petenewcomb/streampool/internal/omnipool"
)

var tdigestPool = omnipool.ForCustom(tdigestTrait{})

type tdigestTrait struct{}

func (tdigestTrait) Make() *tdigest.TDigest {
	return tdigest.New()
}

func (tdigestTrait) Reset(t *tdigest.TDigest) {
	t.Reset()
}

func adoptOrMergeDigest(dst **tdigest.TDigest, src *tdigest.TDigest) {
	if *dst == nil {
		*dst = src
	} else {
		(*dst).Merge(src)
		tdigestPool.Release(src)
	}
}

func addToDigest(dst **tdigest.TDigest, x, w float64) {
	t := *dst
	if t == nil {
		t = tdigestPool.Get()
		*dst = t
	}
	t.Add(x, w)
}

/*
var centroidListPool = omnipool.ForSlice(tdigest.CentroidList(nil))

func newCentroidList(c ...tdigest.Centroid) tdigest.CentroidList {
	cl := centroidListPool.Get()
	cl = append(cl, c...)
	return cl
}
func copyCentroidList(t *tdigest.TDigest) tdigest.CentroidList {
	cl := centroidListPool.Get()
	cl = t.Centroids(cl)
	return cl
}
func poolCentroidList(cl tdigest.CentroidList) {
	centroidListPool.Put(cl)
}
*/
