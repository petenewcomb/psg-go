// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package ema

import (
	"time"
)

type State struct {
	LatestSampleTime time.Time
	EMA              EMA
}

func (st *State) Get() float64 {
	return st.EMA.Get()
}

func (st *State) Project(tau Tau) float64 {
	return st.ProjectAt(time.Now(), tau)
}

func (st *State) ProjectAt(t time.Time, tau Tau) float64 {
	return st.EMA.Project(st.alphaAt(t, tau))
}

func (st *State) Update(tau Tau, sample float64) {
	st.UpdateAt(time.Now(), tau, sample)
}

func (st *State) UpdateAt(t time.Time, tau Tau, sample float64) {
	st.EMA.Update(st.alphaAt(t, tau), sample)
	st.LatestSampleTime = t
}

func (st *State) alphaAt(t time.Time, tau Tau) Alpha {
	return tau.Alpha(t.Sub(st.LatestSampleTime))
}
