// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim_test

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/petenewcomb/psg-go/internal/sim"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestPlanFormatting(t *testing.T) {
	chk := require.New(t)

	testdata := "testdata/rapid/TestPlanFormatting"
	_, err := os.Stat("testdata/rapid/TestPlanFormatting")
	if err == nil {
		chk.Fail(fmt.Sprintf("%q should not exist", testdata))
	} else if !os.IsNotExist(err) {
		chk.NoError(err)
	}

	seed := flag.Lookup("rapid.seed").Value.(flag.Getter)
	oldSeedValue := fmt.Sprint(seed.Get())
	defer chk.NoError(seed.Set(oldSeedValue))
	chk.NoError(seed.Set("123"))

	checks := flag.Lookup("rapid.checks").Value.(flag.Getter)
	oldChecksValue := fmt.Sprint(checks.Get())
	defer chk.NoError(checks.Set(oldChecksValue))
	chk.NoError(checks.Set("1"))

	nofailfile := flag.Lookup("rapid.nofailfile").Value.(flag.Getter)
	oldNofailfileValue := fmt.Sprint(nofailfile.Get())
	defer chk.NoError(nofailfile.Set(oldNofailfileValue))
	chk.NoError(nofailfile.Set("true"))

	expected := `Plan#0: pathCount=6 taskCount=13 maxPathDuration=308.702125ms
Plan#0 step 1/3 (+0s): scatter:
  Task#320: pool=0
  Task#320 step 1/2 (+0s): 99.992µs self time
  Task#320 step 2/2 (+99.992µs): return error
  Task#320 ends at 99.992µs
    Gather#320:
    Gather#320 step 1/4 (+0s): 22.865µs self time
    Gather#320 step 2/4 (+22.865µs): scatter:
      Task#311: pool=0
      Task#311 step 1/2 (+0s): 99.984µs self time
      Task#311 step 2/2 (+99.984µs): return nil
      Task#311 ends at 222.841µs
        Gather#311:
        Gather#311 step 1/2 (+0s): 10µs self time
        Gather#311 step 2/2 (+10µs): return nil
        Gather#311 ends at 232.841µs
    Gather#320 step 3/4 (+22.865µs): 22.866µs self time
    Gather#320 step 4/4 (+45.731µs): return nil
    Gather#320 ends at 145.723µs
Plan#0 step 2/3 (+0s): scatter:
  Task#321: pool=1
  Task#321 step 1/2 (+0s): 100µs self time
  Task#321 step 2/2 (+100µs): return error
  Task#321 ends at 100µs
    Combine#321:
    Combine#321 step 1/8 (+0s): 2.509µs self time
    Combine#321 step 2/8 (+2.509µs): scatter:
      Task#317: pool=1
      Task#317 step 1/2 (+0s): 6.455986ms self time
      Task#317 step 2/2 (+6.455986ms): return nil
      Task#317 ends at 6.558495ms
        Gather#317:
        Gather#317 step 1/4 (+0s): 279.952µs self time
        Gather#317 step 2/4 (+279.952µs): scatter:
          Task#316: pool=1
          Task#316 step 1/2 (+0s): 99.884µs self time
          Task#316 step 2/2 (+99.884µs): return nil
          Task#316 ends at 6.938331ms
            Gather#316:
            Gather#316 step 1/4 (+0s): 1.063481ms self time
            Gather#316 step 2/4 (+1.063481ms): scatter:
              Task#313: pool=0
              Task#313 step 1/2 (+0s): 98.408µs self time
              Task#313 step 2/2 (+98.408µs): return nil
              Task#313 ends at 8.10022ms
                Gather#313:
                Gather#313 step 1/2 (+0s): 10.646µs self time
                Gather#313 step 2/2 (+10.646µs): return nil
                Gather#313 ends at 8.110866ms
            Gather#316 step 3/4 (+1.063481ms): 1.063484ms self time
            Gather#316 step 4/4 (+2.126965ms): return nil
            Gather#316 ends at 9.065296ms
        Gather#317 step 3/4 (+279.952µs): 66.959µs self time
        Gather#317 step 4/4 (+346.911µs): return nil
        Gather#317 ends at 6.905406ms
    Combine#321 step 3/8 (+2.509µs): 2.493µs self time
    Combine#321 step 4/8 (+5.002µs): scatter:
      Task#318: pool=0
      Task#318 step 1/2 (+0s): 100µs self time
      Task#318 step 2/2 (+100µs): return nil
      Task#318 ends at 205.002µs
        Gather#318:
        Gather#318 step 1/4 (+0s): 3.64µs self time
        Gather#318 step 2/4 (+3.64µs): scatter:
          Task#312: pool=1
          Task#312 step 1/2 (+0s): 45.999µs self time
          Task#312 step 2/2 (+45.999µs): return nil
          Task#312 ends at 254.641µs
            Gather#312:
            Gather#312 step 1/2 (+0s): 700.532µs self time
            Gather#312 step 2/2 (+700.532µs): return nil
            Gather#312 ends at 955.173µs
        Gather#318 step 3/4 (+3.64µs): 6.366µs self time
        Gather#318 step 4/4 (+10.006µs): return nil
        Gather#318 ends at 215.008µs
    Combine#321 step 5/8 (+5.002µs): 2.498µs self time
    Combine#321 step 6/8 (+7.5µs): scatter:
      Task#319: pool=0
      Task#319 step 1/2 (+0s): 95.605µs self time
      Task#319 step 2/2 (+95.605µs): return nil
      Task#319 ends at 203.105µs
        Gather#319:
        Gather#319 step 1/6 (+0s): 3.327µs self time
        Gather#319 step 2/6 (+3.327µs): scatter:
          Task#0: pool=1
          Task#0 step 1/4 (+0s): 49.627µs self time
          Task#0 step 2/4 (+49.627µs): subjob:
            Plan#1: pathCount=4 taskCount=11 maxPathDuration=85.322651ms
            Plan#1 step 1/3 (+0s): scatter:
              Task#61: pool=0
              Task#61 step 1/2 (+0s): 99.934µs self time
              Task#61 step 2/2 (+99.934µs): return nil
              Task#61 ends at 99.934µs
                Gather#61:
                Gather#61 step 1/4 (+0s): 4.987µs self time
                Gather#61 step 2/4 (+4.987µs): scatter:
                  Task#3: pool=1
                  Task#3 step 1/2 (+0s): 100.177µs self time
                  Task#3 step 2/2 (+100.177µs): return nil
                  Task#3 ends at 205.098µs
                    Gather#3:
                    Gather#3 step 1/2 (+0s): 10.004µs self time
                    Gather#3 step 2/2 (+10.004µs): return nil
                    Gather#3 ends at 215.102µs
                Gather#61 step 3/4 (+4.987µs): 5.012µs self time
                Gather#61 step 4/4 (+9.999µs): return nil
                Gather#61 ends at 109.933µs
            Plan#1 step 2/3 (+0s): scatter:
              Task#60: pool=0
              Task#60 step 1/2 (+0s): 622.054µs self time
              Task#60 step 2/2 (+622.054µs): return nil
              Task#60 ends at 622.054µs
                Combine#60:
                Combine#60 step 1/8 (+0s): 6.103µs self time
                Combine#60 step 2/8 (+6.103µs): scatter:
                  Task#4: pool=1
                  Task#4 step 1/2 (+0s): 3.554448ms self time
                  Task#4 step 2/2 (+3.554448ms): return nil
                  Task#4 ends at 4.182605ms
                    Gather#4:
                    Gather#4 step 1/2 (+0s): 9.989µs self time
                    Gather#4 step 2/2 (+9.989µs): return nil
                    Gather#4 ends at 4.192594ms
                Combine#60 step 3/8 (+6.103µs): 1.59µs self time
                Combine#60 step 4/8 (+7.693µs): scatter:
                  Task#8: pool=1
                  Task#8 step 1/2 (+0s): 66.973µs self time
                  Task#8 step 2/2 (+66.973µs): return nil
                  Task#8 ends at 696.72µs
                    Gather#8:
                    Gather#8 step 1/6 (+0s): 3.496µs self time
                    Gather#8 step 2/6 (+3.496µs): subjob:
                      Plan#2: pathCount=15 taskCount=26 maxPathDuration=84.495555ms
                      Plan#2 step 1/5 (+0s): scatter:
                        Task#56: pool=0
                        Task#56 step 1/2 (+0s): 100.004µs self time
                        Task#56 step 2/2 (+100.004µs): return nil
                        Task#56 ends at 100.004µs
                          Gather#56:
                          Gather#56 step 1/4 (+0s): 4.778µs self time
                          Gather#56 step 2/4 (+4.778µs): scatter:
                            Task#54: pool=0
                            Task#54 step 1/2 (+0s): 100.478µs self time
                            Task#54 step 2/2 (+100.478µs): return nil
                            Task#54 ends at 205.26µs
                              Gather#54:
                              Gather#54 step 1/6 (+0s): 498.515µs self time
                              Gather#54 step 2/6 (+498.515µs): scatter:
                                Task#37: pool=0
                                Task#37 step 1/2 (+0s): 99.999µs self time
                                Task#37 step 2/2 (+99.999µs): return nil
                                Task#37 ends at 803.774µs
                                  Gather#37:
                                  Gather#37 step 1/8 (+0s): 3.262µs self time
                                  Gather#37 step 2/8 (+3.262µs): scatter:
                                    Task#36: pool=0
                                    Task#36 step 1/2 (+0s): 99.975µs self time
                                    Task#36 step 2/2 (+99.975µs): return nil
                                    Task#36 ends at 907.011µs
                                      Combine#36:
                                      Combine#36 step 1/4 (+0s): 5.008µs self time
                                      Combine#36 step 2/4 (+5.008µs): scatter:
                                        Task#10: pool=0
                                        Task#10 step 1/2 (+0s): 100.041µs self time
                                        Task#10 step 2/2 (+100.041µs): return nil
                                        Task#10 ends at 1.01206ms
                                          Gather#10:
                                          Gather#10 step 1/2 (+0s): 9.997µs self time
                                          Gather#10 step 2/2 (+9.997µs): return nil
                                          Gather#10 ends at 1.022057ms
                                      Combine#36 step 3/4 (+5.008µs): 4.99µs self time
                                      Combine#36 step 4/4 (+9.998µs): return nil
                                      Combine#36 ends at 917.009µs
                                  Gather#37 step 3/8 (+3.262µs): 6.011µs self time
                                  Gather#37 step 4/8 (+9.273µs): scatter:
                                    Task#9: pool=0
                                    Task#9 step 1/2 (+0s): 98.577µs self time
                                    Task#9 step 2/2 (+98.577µs): return error
                                    Task#9 ends at 911.624µs
                                      Gather#9:
                                      Gather#9 step 1/2 (+0s): 10.055µs self time
                                      Gather#9 step 2/2 (+10.055µs): return error
                                      Gather#9 ends at 921.679µs
                                  Gather#37 step 5/8 (+9.273µs): 360ns self time
                                  Gather#37 step 6/8 (+9.633µs): scatter:
                                    Task#11: pool=0
                                    Task#11 step 1/2 (+0s): 99.989µs self time
                                    Task#11 step 2/2 (+99.989µs): return nil
                                    Task#11 ends at 913.396µs
                                      Gather#11:
                                      Gather#11 step 1/2 (+0s): 9.961µs self time
                                      Gather#11 step 2/2 (+9.961µs): return nil
                                      Gather#11 ends at 923.357µs
                                  Gather#37 step 7/8 (+9.633µs): 364ns self time
                                  Gather#37 step 8/8 (+9.997µs): return nil
                                  Gather#37 ends at 813.771µs
                              Gather#54 step 3/6 (+498.515µs): 499.073µs self time
                              Gather#54 step 4/6 (+997.588µs): scatter:
                                Task#38: pool=0
                                Task#38 step 1/2 (+0s): 100.148µs self time
                                Task#38 step 2/2 (+100.148µs): return nil
                                Task#38 ends at 1.302996ms
                                  Gather#38:
                                  Gather#38 step 1/4 (+0s): 28.205µs self time
                                  Gather#38 step 2/4 (+28.205µs): scatter:
                                    Task#35: pool=0
                                    Task#35 step 1/2 (+0s): 100.107µs self time
                                    Task#35 step 2/2 (+100.107µs): return nil
                                    Task#35 ends at 1.431308ms
                                      Gather#35:
                                      Gather#35 step 1/6 (+0s): 3.526µs self time
                                      Gather#35 step 2/6 (+3.526µs): scatter:
                                        Task#34: pool=0
                                        Task#34 step 1/2 (+0s): 99.97µs self time
                                        Task#34 step 2/2 (+99.97µs): return nil
                                        Task#34 ends at 1.534804ms
                                          Gather#34:
                                          Gather#34 step 1/6 (+0s): 2.976µs self time
                                          Gather#34 step 2/6 (+2.976µs): scatter:
                                            Task#27: pool=0
                                            Task#27 step 1/2 (+0s): 99.031µs self time
                                            Task#27 step 2/2 (+99.031µs): return nil
                                            Task#27 ends at 1.636811ms
                                              Gather#27:
                                              Gather#27 step 1/2 (+0s): 10µs self time
                                              Gather#27 step 2/2 (+10µs): return nil
                                              Gather#27 ends at 1.646811ms
                                          Gather#34 step 3/6 (+2.976µs): 0s self time
                                          Gather#34 step 4/6 (+2.976µs): scatter:
                                            Task#25: pool=0
                                            Task#25 step 1/2 (+0s): 99.971µs self time
                                            Task#25 step 2/2 (+99.971µs): return nil
                                            Task#25 ends at 1.637751ms
                                              Gather#25:
                                              Gather#25 step 1/2 (+0s): 30.672µs self time
                                              Gather#25 step 2/2 (+30.672µs): return error
                                              Gather#25 ends at 1.668423ms
                                          Gather#34 step 5/6 (+2.976µs): 0s self time
                                          Gather#34 step 6/6 (+2.976µs): return nil
                                          Gather#34 ends at 1.53778ms
                                      Gather#35 step 3/6 (+3.526µs): 4.281µs self time
                                      Gather#35 step 4/6 (+7.807µs): scatter:
                                        Task#30: pool=0
                                        Task#30 step 1/2 (+0s): 99.998µs self time
                                        Task#30 step 2/2 (+99.998µs): return nil
                                        Task#30 ends at 1.539113ms
                                          Combine#30:
                                          Combine#30 step 1/2 (+0s): 10.524µs self time
                                          Combine#30 step 2/2 (+10.524µs): return nil
                                          Combine#30 ends at 1.549637ms
                                      Gather#35 step 5/6 (+7.807µs): 2.775µs self time
                                      Gather#35 step 6/6 (+10.582µs): return nil
                                      Gather#35 ends at 1.44189ms
                                  Gather#38 step 3/4 (+28.205µs): 28.236µs self time
                                  Gather#38 step 4/4 (+56.441µs): return nil
                                  Gather#38 ends at 1.359437ms
                              Gather#54 step 5/6 (+997.588µs): 498.485µs self time
                              Gather#54 step 6/6 (+1.496073ms): return nil
                              Gather#54 ends at 1.701333ms
                          Gather#56 step 3/4 (+4.778µs): 5.22µs self time
                          Gather#56 step 4/4 (+9.998µs): return nil
                          Gather#56 ends at 110.002µs
                      Plan#2 step 2/5 (+0s): scatter:
                        Task#57: pool=0
                        Task#57 step 1/2 (+0s): 99.903µs self time
                        Task#57 step 2/2 (+99.903µs): return nil
                        Task#57 ends at 99.903µs
                          Gather#57:
                          Gather#57 step 1/6 (+0s): 755.639µs self time
                          Gather#57 step 2/6 (+755.639µs): scatter:
                            Task#12: pool=0
                            Task#12 step 1/2 (+0s): 97.133µs self time
                            Task#12 step 2/2 (+97.133µs): return error
                            Task#12 ends at 952.675µs
                              Gather#12:
                              Gather#12 step 1/2 (+0s): 10.072µs self time
                              Gather#12 step 2/2 (+10.072µs): return nil
                              Gather#12 ends at 962.747µs
                          Gather#57 step 3/6 (+755.639µs): 1.434517ms self time
                          Gather#57 step 4/6 (+2.190156ms): scatter:
                            Task#32: pool=0
                            Task#32 step 1/2 (+0s): 82.199471ms self time
                            Task#32 step 2/2 (+82.199471ms): return nil
                            Task#32 ends at 84.48953ms
                              Combine#32:
                              Combine#32 step 1/2 (+0s): 6.025µs self time
                              Combine#32 step 2/2 (+6.025µs): return nil
                              Combine#32 ends at 84.495555ms
                          Gather#57 step 5/6 (+2.190156ms): 76.806µs self time
                          Gather#57 step 6/6 (+2.266962ms): return nil
                          Gather#57 ends at 2.366865ms
                      Plan#2 step 3/5 (+0s): scatter:
                        Task#58: pool=0
                        Task#58 step 1/2 (+0s): 98.522µs self time
                        Task#58 step 2/2 (+98.522µs): return nil
                        Task#58 ends at 98.522µs
                          Gather#58:
                          Gather#58 step 1/4 (+0s): 25ns self time
                          Gather#58 step 2/4 (+25ns): scatter:
                            Task#33: pool=0
                            Task#33 step 1/2 (+0s): 46.617µs self time
                            Task#33 step 2/2 (+46.617µs): return nil
                            Task#33 ends at 145.164µs
                              Gather#33:
                              Gather#33 step 1/2 (+0s): 12.211µs self time
                              Gather#33 step 2/2 (+12.211µs): return nil
                              Gather#33 ends at 157.375µs
                          Gather#58 step 3/4 (+25ns): 678ns self time
                          Gather#58 step 4/4 (+703ns): return nil
                          Gather#58 ends at 99.225µs
                      Plan#2 step 4/5 (+0s): scatter:
                        Task#55: pool=0
                        Task#55 step 1/2 (+0s): 2.072834ms self time
                        Task#55 step 2/2 (+2.072834ms): return nil
                        Task#55 ends at 2.072834ms
                          Gather#55:
                          Gather#55 step 1/14 (+0s): 1.456µs self time
                          Gather#55 step 2/14 (+1.456µs): scatter:
                            Task#26: pool=0
                            Task#26 step 1/2 (+0s): 135.737µs self time
                            Task#26 step 2/2 (+135.737µs): return nil
                            Task#26 ends at 2.210027ms
                              Gather#26:
                              Gather#26 step 1/2 (+0s): 4.809125ms self time
                              Gather#26 step 2/2 (+4.809125ms): return nil
                              Gather#26 ends at 7.019152ms
                          Gather#55 step 3/14 (+1.456µs): 1.425µs self time
                          Gather#55 step 4/14 (+2.881µs): scatter:
                            Task#28: pool=0
                            Task#28 step 1/2 (+0s): 100.042µs self time
                            Task#28 step 2/2 (+100.042µs): return nil
                            Task#28 ends at 2.175757ms
                              Gather#28:
                              Gather#28 step 1/2 (+0s): 9.983µs self time
                              Gather#28 step 2/2 (+9.983µs): return nil
                              Gather#28 ends at 2.18574ms
                          Gather#55 step 5/14 (+2.881µs): 1.421µs self time
                          Gather#55 step 6/14 (+4.302µs): scatter:
                            Task#39: pool=0
                            Task#39 step 1/4 (+0s): 49.274µs self time
                            Task#39 step 2/4 (+49.274µs): subjob:
                              Plan#4: pathCount=6 taskCount=14 maxPathDuration=8.317243ms
                              Plan#4 step 1/2 (+0s): scatter:
                                Task#53: pool=8
                                Task#53 step 1/2 (+0s): 107.266µs self time
                                Task#53 step 2/2 (+107.266µs): return error
                                Task#53 ends at 107.266µs
                                  Gather#53:
                                  Gather#53 step 1/4 (+0s): 5.081µs self time
                                  Gather#53 step 2/4 (+5.081µs): scatter:
                                    Task#52: pool=0
                                    Task#52 step 1/2 (+0s): 99.991µs self time
                                    Task#52 step 2/2 (+99.991µs): return nil
                                    Task#52 ends at 212.338µs
                                      Gather#52:
                                      Gather#52 step 1/12 (+0s): 1.698µs self time
                                      Gather#52 step 2/12 (+1.698µs): scatter:
                                        Task#51: pool=5
                                        Task#51 step 1/2 (+0s): 100.113µs self time
                                        Task#51 step 2/2 (+100.113µs): return nil
                                        Task#51 ends at 314.149µs
                                          Gather#51:
                                          Gather#51 step 1/6 (+0s): 3.336µs self time
                                          Gather#51 step 2/6 (+3.336µs): scatter:
                                            Task#43: pool=0
                                            Task#43 step 1/2 (+0s): 100.036µs self time
                                            Task#43 step 2/2 (+100.036µs): return nil
                                            Task#43 ends at 417.521µs
                                              Gather#43:
                                              Gather#43 step 1/2 (+0s): 9.996µs self time
                                              Gather#43 step 2/2 (+9.996µs): return nil
                                              Gather#43 ends at 427.517µs
                                          Gather#51 step 3/6 (+3.336µs): 3.308µs self time
                                          Gather#51 step 4/6 (+6.644µs): scatter:
                                            Task#42: pool=7
                                            Task#42 step 1/2 (+0s): 101.808µs self time
                                            Task#42 step 2/2 (+101.808µs): return nil
                                            Task#42 ends at 422.601µs
                                              Gather#42:
                                              Gather#42 step 1/2 (+0s): 9.957µs self time
                                              Gather#42 step 2/2 (+9.957µs): return nil
                                              Gather#42 ends at 432.558µs
                                          Gather#51 step 5/6 (+6.644µs): 3.363µs self time
                                          Gather#51 step 6/6 (+10.007µs): return nil
                                          Gather#51 ends at 324.156µs
                                      Gather#52 step 3/12 (+1.698µs): 1.486µs self time
                                      Gather#52 step 4/12 (+3.184µs): scatter:
                                        Task#45: pool=1
                                        Task#45 step 1/2 (+0s): 99.999µs self time
                                        Task#45 step 2/2 (+99.999µs): return nil
                                        Task#45 ends at 315.521µs
                                          Gather#45:
                                          Gather#45 step 1/2 (+0s): 10µs self time
                                          Gather#45 step 2/2 (+10µs): return nil
                                          Gather#45 ends at 325.521µs
                                      Gather#52 step 5/12 (+3.184µs): 2.717µs self time
                                      Gather#52 step 6/12 (+5.901µs): scatter:
                                        Task#50: pool=5
                                        Task#50 step 1/2 (+0s): 99.996µs self time
                                        Task#50 step 2/2 (+99.996µs): return error
                                        Task#50 ends at 318.235µs
                                          Gather#50:
                                          Gather#50 step 1/4 (+0s): 6.118µs self time
                                          Gather#50 step 2/4 (+6.118µs): scatter:
                                            Task#47: pool=7
                                            Task#47 step 1/2 (+0s): 7.682755ms self time
                                            Task#47 step 2/2 (+7.682755ms): return nil
                                            Task#47 ends at 8.007108ms
                                              Gather#47:
                                              Gather#47 step 1/4 (+0s): 4.393µs self time
                                              Gather#47 step 2/4 (+4.393µs): scatter:
                                                Task#46: pool=1
                                                Task#46 step 1/2 (+0s): 193.684µs self time
                                                Task#46 step 2/2 (+193.684µs): return nil
                                                Task#46 ends at 8.205185ms
                                                  Gather#46:
                                                  Gather#46 step 1/4 (+0s): 5.971µs self time
                                                  Gather#46 step 2/4 (+5.971µs): scatter:
                                                    Task#44: pool=1
                                                    Task#44 step 1/2 (+0s): 99.193µs self time
                                                    Task#44 step 2/2 (+99.193µs): return error
                                                    Task#44 ends at 8.310349ms
                                                      Gather#44:
                                                      Gather#44 step 1/2 (+0s): 6.894µs self time
                                                      Gather#44 step 2/2 (+6.894µs): return nil
                                                      Gather#44 ends at 8.317243ms
                                                  Gather#46 step 3/4 (+5.971µs): 5.151µs self time
                                                  Gather#46 step 4/4 (+11.122µs): return nil
                                                  Gather#46 ends at 8.216307ms
                                              Gather#47 step 3/4 (+4.393µs): 6.511µs self time
                                              Gather#47 step 4/4 (+10.904µs): return nil
                                              Gather#47 ends at 8.018012ms
                                          Gather#50 step 3/4 (+6.118µs): 6.266µs self time
                                          Gather#50 step 4/4 (+12.384µs): return nil
                                          Gather#50 ends at 330.619µs
                                      Gather#52 step 7/12 (+5.901µs): 1.687µs self time
                                      Gather#52 step 8/12 (+7.588µs): scatter:
                                        Task#41: pool=0
                                        Task#41 step 1/2 (+0s): 115.418µs self time
                                        Task#41 step 2/2 (+115.418µs): return nil
                                        Task#41 ends at 335.344µs
                                          Gather#41:
                                          Gather#41 step 1/2 (+0s): 999ns self time
                                          Gather#41 step 2/2 (+999ns): return nil
                                          Gather#41 ends at 336.343µs
                                      Gather#52 step 9/12 (+7.588µs): 1.211µs self time
                                      Gather#52 step 10/12 (+8.799µs): scatter:
                                        Task#49: pool=9
                                        Task#49 step 1/2 (+0s): 99.932µs self time
                                        Task#49 step 2/2 (+99.932µs): return nil
                                        Task#49 ends at 321.069µs
                                          Gather#49:
                                          Gather#49 step 1/4 (+0s): 572ns self time
                                          Gather#49 step 2/4 (+572ns): scatter:
                                            Task#48: pool=5
                                            Task#48 step 1/2 (+0s): 108.67µs self time
                                            Task#48 step 2/2 (+108.67µs): return nil
                                            Task#48 ends at 430.311µs
                                              Gather#48:
                                              Gather#48 step 1/4 (+0s): 5.113µs self time
                                              Gather#48 step 2/4 (+5.113µs): scatter:
                                                Task#40: pool=4
                                                Task#40 step 1/2 (+0s): 100.04µs self time
                                                Task#40 step 2/2 (+100.04µs): return nil
                                                Task#40 ends at 535.464µs
                                                  Gather#40:
                                                  Gather#40 step 1/2 (+0s): 2.347µs self time
                                                  Gather#40 step 2/2 (+2.347µs): return nil
                                                  Gather#40 ends at 537.811µs
                                              Gather#48 step 3/4 (+5.113µs): 5.111µs self time
                                              Gather#48 step 4/4 (+10.224µs): return nil
                                              Gather#48 ends at 440.535µs
                                          Gather#49 step 3/4 (+572ns): 577ns self time
                                          Gather#49 step 4/4 (+1.149µs): return nil
                                          Gather#49 ends at 322.218µs
                                      Gather#52 step 11/12 (+8.799µs): 1.21µs self time
                                      Gather#52 step 12/12 (+10.009µs): return nil
                                      Gather#52 ends at 222.347µs
                                  Gather#53 step 3/4 (+5.081µs): 5.083µs self time
                                  Gather#53 step 4/4 (+10.164µs): return nil
                                  Gather#53 ends at 117.43µs
                              Plan#4 step 2/2 (+0s): ends at 8.317243ms
                            Task#39 step 3/4 (+8.366517ms): 50.152µs self time
                            Task#39 step 4/4 (+8.416669ms): return nil
                            Task#39 ends at 10.493805ms
                              Gather#39:
                              Gather#39 step 1/4 (+0s): 0s self time
                              Gather#39 step 2/4 (+0s): scatter:
                                Task#29: pool=0
                                Task#29 step 1/2 (+0s): 100.009µs self time
                                Task#29 step 2/2 (+100.009µs): return nil
                                Task#29 ends at 10.593814ms
                                  Gather#29:
                                  Gather#29 step 1/2 (+0s): 10.001µs self time
                                  Gather#29 step 2/2 (+10.001µs): return nil
                                  Gather#29 ends at 10.603815ms
                              Gather#39 step 3/4 (+0s): 0s self time
                              Gather#39 step 4/4 (+0s): return nil
                              Gather#39 ends at 10.493805ms
                          Gather#55 step 7/14 (+4.302µs): 2.338µs self time
                          Gather#55 step 8/14 (+6.64µs): scatter:
                            Task#24: pool=0
                            Task#24 step 1/2 (+0s): 2.366567ms self time
                            Task#24 step 2/2 (+2.366567ms): return nil
                            Task#24 ends at 4.446041ms
                              Gather#24:
                              Gather#24 step 1/2 (+0s): 9.987µs self time
                              Gather#24 step 2/2 (+9.987µs): return nil
                              Gather#24 ends at 4.456028ms
                          Gather#55 step 9/14 (+6.64µs): 558ns self time
                          Gather#55 step 10/14 (+7.198µs): scatter:
                            Task#31: pool=0
                            Task#31 step 1/2 (+0s): 100.002µs self time
                            Task#31 step 2/2 (+100.002µs): return nil
                            Task#31 ends at 2.180034ms
                              Gather#31:
                              Gather#31 step 1/2 (+0s): 2.063262ms self time
                              Gather#31 step 2/2 (+2.063262ms): return nil
                              Gather#31 ends at 4.243296ms
                          Gather#55 step 11/14 (+7.198µs): 1.08µs self time
                          Gather#55 step 12/14 (+8.278µs): scatter:
                            Task#13: pool=0
                            Task#13 step 1/2 (+0s): 98.622µs self time
                            Task#13 step 2/2 (+98.622µs): return nil
                            Task#13 ends at 2.179734ms
                              Gather#13:
                              Gather#13 step 1/4 (+0s): 4.888µs self time
                              Gather#13 step 2/4 (+4.888µs): subjob:
                                Plan#3: pathCount=4 taskCount=10 maxPathDuration=10.198198ms
                                Plan#3 step 1/3 (+0s): scatter:
                                  Task#23: pool=0
                                  Task#23 step 1/2 (+0s): 94.835µs self time
                                  Task#23 step 2/2 (+94.835µs): return nil
                                  Task#23 ends at 94.835µs
                                    Combine#23:
                                    Combine#23 step 1/6 (+0s): 3.362µs self time
                                    Combine#23 step 2/6 (+3.362µs): scatter:
                                      Task#15: pool=0
                                      Task#15 step 1/2 (+0s): 100.001µs self time
                                      Task#15 step 2/2 (+100.001µs): return nil
                                      Task#15 ends at 198.198µs
                                        Gather#15:
                                        Gather#15 step 1/2 (+0s): 10ms self time
                                        Gather#15 step 2/2 (+10ms): return nil
                                        Gather#15 ends at 10.198198ms
                                    Combine#23 step 3/6 (+3.362µs): 3.313µs self time
                                    Combine#23 step 4/6 (+6.675µs): scatter:
                                      Task#20: pool=0
                                      Task#20 step 1/2 (+0s): 532.565µs self time
                                      Task#20 step 2/2 (+532.565µs): return nil
                                      Task#20 ends at 634.075µs
                                        Gather#20:
                                        Gather#20 step 1/4 (+0s): 9.992µs self time
                                        Gather#20 step 2/4 (+9.992µs): scatter:
                                          Task#19: pool=0
                                          Task#19 step 1/2 (+0s): 99.999µs self time
                                          Task#19 step 2/2 (+99.999µs): return nil
                                          Task#19 ends at 744.066µs
                                            Gather#19:
                                            Gather#19 step 1/4 (+0s): 4.984µs self time
                                            Gather#19 step 2/4 (+4.984µs): scatter:
                                              Task#18: pool=0
                                              Task#18 step 1/2 (+0s): 99.77µs self time
                                              Task#18 step 2/2 (+99.77µs): return nil
                                              Task#18 ends at 848.82µs
                                                Gather#18:
                                                Gather#18 step 1/6 (+0s): 3.299µs self time
                                                Gather#18 step 2/6 (+3.299µs): scatter:
                                                  Task#17: pool=0
                                                  Task#17 step 1/2 (+0s): 99.999µs self time
                                                  Task#17 step 2/2 (+99.999µs): return nil
                                                  Task#17 ends at 952.118µs
                                                    Gather#17:
                                                    Gather#17 step 1/2 (+0s): 342.422µs self time
                                                    Gather#17 step 2/2 (+342.422µs): return nil
                                                    Gather#17 ends at 1.29454ms
                                                Gather#18 step 3/6 (+3.299µs): 1.904µs self time
                                                Gather#18 step 4/6 (+5.203µs): scatter:
                                                  Task#16: pool=0
                                                  Task#16 step 1/2 (+0s): 99.992µs self time
                                                  Task#16 step 2/2 (+99.992µs): return nil
                                                  Task#16 ends at 954.015µs
                                                    Gather#16:
                                                    Gather#16 step 1/2 (+0s): 9.991µs self time
                                                    Gather#16 step 2/2 (+9.991µs): return nil
                                                    Gather#16 ends at 964.006µs
                                                Gather#18 step 5/6 (+5.203µs): 4.852µs self time
                                                Gather#18 step 6/6 (+10.055µs): return nil
                                                Gather#18 ends at 858.875µs
                                            Gather#19 step 3/4 (+4.984µs): 4.972µs self time
                                            Gather#19 step 4/4 (+9.956µs): return nil
                                            Gather#19 ends at 754.022µs
                                        Gather#20 step 3/4 (+9.992µs): 10.011µs self time
                                        Gather#20 step 4/4 (+20.003µs): return nil
                                        Gather#20 ends at 654.078µs
                                    Combine#23 step 5/6 (+6.675µs): 3.327µs self time
                                    Combine#23 step 6/6 (+10.002µs): return nil
                                    Combine#23 ends at 104.837µs
                                Plan#3 step 2/3 (+0s): scatter:
                                  Task#22: pool=0
                                  Task#22 step 1/2 (+0s): 0s self time
                                  Task#22 step 2/2 (+0s): return nil
                                  Task#22 ends at 0s
                                    Gather#22:
                                    Gather#22 step 1/4 (+0s): 5.012µs self time
                                    Gather#22 step 2/4 (+5.012µs): scatter:
                                      Task#21: pool=0
                                      Task#21 step 1/2 (+0s): 99.992µs self time
                                      Task#21 step 2/2 (+99.992µs): return nil
                                      Task#21 ends at 105.004µs
                                        Gather#21:
                                        Gather#21 step 1/4 (+0s): 5.007µs self time
                                        Gather#21 step 2/4 (+5.007µs): scatter:
                                          Task#14: pool=0
                                          Task#14 step 1/2 (+0s): 99.997µs self time
                                          Task#14 step 2/2 (+99.997µs): return nil
                                          Task#14 ends at 210.008µs
                                            Gather#14:
                                            Gather#14 step 1/2 (+0s): 9.999µs self time
                                            Gather#14 step 2/2 (+9.999µs): return nil
                                            Gather#14 ends at 220.007µs
                                        Gather#21 step 3/4 (+5.007µs): 4.992µs self time
                                        Gather#21 step 4/4 (+9.999µs): return nil
                                        Gather#21 ends at 115.003µs
                                    Gather#22 step 3/4 (+5.012µs): 5.012µs self time
                                    Gather#22 step 4/4 (+10.024µs): return nil
                                    Gather#22 ends at 10.024µs
                                Plan#3 step 3/3 (+0s): ends at 10.198198ms
                              Gather#13 step 3/4 (+10.203086ms): 4.89µs self time
                              Gather#13 step 4/4 (+10.207976ms): return nil
                              Gather#13 ends at 12.38771ms
                          Gather#55 step 13/14 (+8.278µs): 1.725µs self time
                          Gather#55 step 14/14 (+10.003µs): return nil
                          Gather#55 ends at 2.082837ms
                      Plan#2 step 5/5 (+0s): ends at 84.495555ms
                    Gather#8 step 3/6 (+84.499051ms): 5.384µs self time
                    Gather#8 step 4/6 (+84.504435ms): scatter:
                      Task#7: pool=0
                      Task#7 step 1/2 (+0s): 6.965µs self time
                      Task#7 step 2/2 (+6.965µs): return error
                      Task#7 ends at 85.20812ms
                        Gather#7:
                        Gather#7 step 1/4 (+0s): 4.539µs self time
                        Gather#7 step 2/4 (+4.539µs): scatter:
                          Task#2: pool=0
                          Task#2 step 1/2 (+0s): 99.998µs self time
                          Task#2 step 2/2 (+99.998µs): return nil
                          Task#2 ends at 85.312657ms
                            Gather#2:
                            Gather#2 step 1/2 (+0s): 9.994µs self time
                            Gather#2 step 2/2 (+9.994µs): return nil
                            Gather#2 ends at 85.322651ms
                        Gather#7 step 3/4 (+4.539µs): 4.404µs self time
                        Gather#7 step 4/4 (+8.943µs): return nil
                        Gather#7 ends at 85.217063ms
                    Gather#8 step 5/6 (+84.504435ms): 1.616µs self time
                    Gather#8 step 6/6 (+84.506051ms): return nil
                    Gather#8 ends at 85.202771ms
                Combine#60 step 5/8 (+7.693µs): 736ns self time
                Combine#60 step 6/8 (+8.429µs): scatter:
                  Task#59: pool=1
                  Task#59 step 1/2 (+0s): 93.472µs self time
                  Task#59 step 2/2 (+93.472µs): return nil
                  Task#59 ends at 723.955µs
                    Gather#59:
                    Gather#59 step 1/4 (+0s): 10.002µs self time
                    Gather#59 step 2/4 (+10.002µs): scatter:
                      Task#6: pool=1
                      Task#6 step 1/2 (+0s): 100.001µs self time
                      Task#6 step 2/2 (+100.001µs): return nil
                      Task#6 ends at 833.958µs
                        Gather#6:
                        Gather#6 step 1/4 (+0s): 5.001µs self time
                        Gather#6 step 2/4 (+5.001µs): scatter:
                          Task#5: pool=1
                          Task#5 step 1/2 (+0s): 99.999µs self time
                          Task#5 step 2/2 (+99.999µs): return nil
                          Task#5 ends at 938.958µs
                            Gather#5:
                            Gather#5 step 1/4 (+0s): 5.225µs self time
                            Gather#5 step 2/4 (+5.225µs): scatter:
                              Task#1: pool=1
                              Task#1 step 1/2 (+0s): 100.012µs self time
                              Task#1 step 2/2 (+100.012µs): return nil
                              Task#1 ends at 1.044195ms
                                Gather#1:
                                Gather#1 step 1/2 (+0s): 10µs self time
                                Gather#1 step 2/2 (+10µs): return nil
                                Gather#1 ends at 1.054195ms
                            Gather#5 step 3/4 (+5.225µs): 4.857µs self time
                            Gather#5 step 4/4 (+10.082µs): return nil
                            Gather#5 ends at 949.04µs
                        Gather#6 step 3/4 (+5.001µs): 5µs self time
                        Gather#6 step 4/4 (+10.001µs): return nil
                        Gather#6 ends at 843.959µs
                    Gather#59 step 3/4 (+10.002µs): 0s self time
                    Gather#59 step 4/4 (+10.002µs): return nil
                    Gather#59 ends at 733.957µs
                Combine#60 step 7/8 (+8.429µs): 2.451µs self time
                Combine#60 step 8/8 (+10.88µs): return nil
                Combine#60 ends at 632.934µs
            Plan#1 step 3/3 (+0s): ends at 85.322651ms
          Task#0 step 3/4 (+85.372278ms): 50.145µs self time
          Task#0 step 4/4 (+85.422423ms): return nil
          Task#0 ends at 85.628855ms
            Combine#0:
            Combine#0 step 1/2 (+0s): 4.489ms self time
            Combine#0 step 2/2 (+4.489ms): return nil
            Combine#0 ends at 90.117855ms
        Gather#319 step 3/6 (+3.327µs): 3.338µs self time
        Gather#319 step 4/6 (+6.665µs): scatter:
          Task#315: pool=1
          Task#315 step 1/2 (+0s): 20.437µs self time
          Task#315 step 2/2 (+20.437µs): return nil
          Task#315 ends at 230.207µs
            Gather#315:
            Gather#315 step 1/6 (+0s): 1.956µs self time
            Gather#315 step 2/6 (+1.956µs): scatter:
              Task#62: pool=0
              Task#62 step 1/2 (+0s): 99.767µs self time
              Task#62 step 2/2 (+99.767µs): return nil
              Task#62 ends at 331.93µs
                Gather#62:
                Gather#62 step 1/4 (+0s): 5.057µs self time
                Gather#62 step 2/4 (+5.057µs): subjob:
                  Plan#5: pathCount=19 taskCount=29 maxPathDuration=308.360135ms
                  Plan#5 step 1/3 (+0s): scatter:
                    Task#309: pool=0
                    Task#309 step 1/2 (+0s): 98.112µs self time
                    Task#309 step 2/2 (+98.112µs): return nil
                    Task#309 ends at 98.112µs
                      Gather#309:
                      Gather#309 step 1/4 (+0s): 3.441µs self time
                      Gather#309 step 2/4 (+3.441µs): scatter:
                        Task#85: pool=0
                        Task#85 step 1/2 (+0s): 74.398µs self time
                        Task#85 step 2/2 (+74.398µs): return nil
                        Task#85 ends at 175.951µs
                          Gather#85:
                          Gather#85 step 1/2 (+0s): 10.002µs self time
                          Gather#85 step 2/2 (+10.002µs): return nil
                          Gather#85 ends at 185.953µs
                      Gather#309 step 3/4 (+3.441µs): 3.442µs self time
                      Gather#309 step 4/4 (+6.883µs): return nil
                      Gather#309 ends at 104.995µs
                  Plan#5 step 2/3 (+0s): scatter:
                    Task#310: pool=0
                    Task#310 step 1/2 (+0s): 1.80617ms self time
                    Task#310 step 2/2 (+1.80617ms): return nil
                    Task#310 ends at 1.80617ms
                      Gather#310:
                      Gather#310 step 1/14 (+0s): 877ns self time
                      Gather#310 step 2/14 (+877ns): scatter:
                        Task#185: pool=0
                        Task#185 step 1/2 (+0s): 99.973µs self time
                        Task#185 step 2/2 (+99.973µs): return nil
                        Task#185 ends at 1.90702ms
                          Gather#185:
                          Gather#185 step 1/2 (+0s): 9.996µs self time
                          Gather#185 step 2/2 (+9.996µs): return nil
                          Gather#185 ends at 1.917016ms
                      Gather#310 step 3/14 (+877ns): 917ns self time
                      Gather#310 step 4/14 (+1.794µs): scatter:
                        Task#186: pool=0
                        Task#186 step 1/2 (+0s): 352.959µs self time
                        Task#186 step 2/2 (+352.959µs): return nil
                        Task#186 ends at 2.160923ms
                          Gather#186:
                          Gather#186 step 1/2 (+0s): 3.288µs self time
                          Gather#186 step 2/2 (+3.288µs): return nil
                          Gather#186 ends at 2.164211ms
                      Gather#310 step 5/14 (+1.794µs): 766ns self time
                      Gather#310 step 6/14 (+2.56µs): scatter:
                        Task#139: pool=0
                        Task#139 step 1/2 (+0s): 100.459µs self time
                        Task#139 step 2/2 (+100.459µs): return nil
                        Task#139 ends at 1.909189ms
                          Gather#139:
                          Gather#139 step 1/2 (+0s): 7.816µs self time
                          Gather#139 step 2/2 (+7.816µs): return nil
                          Gather#139 ends at 1.917005ms
                      Gather#310 step 7/14 (+2.56µs): 814ns self time
                      Gather#310 step 8/14 (+3.374µs): scatter:
                        Task#195: pool=0
                        Task#195 step 1/4 (+0s): 19.948µs self time
                        Task#195 step 2/4 (+19.948µs): subjob:
                          Plan#17: pathCount=10 taskCount=18 maxPathDuration=102.946778ms
                          Plan#17 step 1/2 (+0s): scatter:
                            Task#307: pool=1
                            Task#307 step 1/2 (+0s): 99.988µs self time
                            Task#307 step 2/2 (+99.988µs): return nil
                            Task#307 ends at 99.988µs
                              Gather#307:
                              Gather#307 step 1/6 (+0s): 2.485µs self time
                              Gather#307 step 2/6 (+2.485µs): scatter:
                                Task#222: pool=1
                                Task#222 step 1/2 (+0s): 99.966µs self time
                                Task#222 step 2/2 (+99.966µs): return nil
                                Task#222 ends at 202.439µs
                                  Gather#222:
                                  Gather#222 step 1/2 (+0s): 78.783µs self time
                                  Gather#222 step 2/2 (+78.783µs): return nil
                                  Gather#222 ends at 281.222µs
                              Gather#307 step 3/6 (+2.485µs): 2.305µs self time
                              Gather#307 step 4/6 (+4.79µs): scatter:
                                Task#306: pool=1
                                Task#306 step 1/2 (+0s): 14.882µs self time
                                Task#306 step 2/2 (+14.882µs): return nil
                                Task#306 ends at 119.66µs
                                  Combine#306:
                                  Combine#306 step 1/14 (+0s): 2.576µs self time
                                  Combine#306 step 2/14 (+2.576µs): scatter:
                                    Task#262: pool=0
                                    Task#262 step 1/2 (+0s): 96µs self time
                                    Task#262 step 2/2 (+96µs): return nil
                                    Task#262 ends at 218.236µs
                                      Gather#262:
                                      Gather#262 step 1/2 (+0s): 10.999µs self time
                                      Gather#262 step 2/2 (+10.999µs): return nil
                                      Gather#262 ends at 229.235µs
                                  Combine#306 step 3/14 (+2.576µs): 2.574µs self time
                                  Combine#306 step 4/14 (+5.15µs): scatter:
                                    Task#305: pool=1
                                    Task#305 step 1/2 (+0s): 101.583µs self time
                                    Task#305 step 2/2 (+101.583µs): return nil
                                    Task#305 ends at 226.393µs
                                      Gather#305:
                                      Gather#305 step 1/8 (+0s): 10.013µs self time
                                      Gather#305 step 2/8 (+10.013µs): scatter:
                                        Task#303: pool=0
                                        Task#303 step 1/2 (+0s): 56.307µs self time
                                        Task#303 step 2/2 (+56.307µs): return nil
                                        Task#303 ends at 292.713µs
                                          Gather#303:
                                          Gather#303 step 1/6 (+0s): 1.766µs self time
                                          Gather#303 step 2/6 (+1.766µs): scatter:
                                            Task#270: pool=1
                                            Task#270 step 1/2 (+0s): 100.004µs self time
                                            Task#270 step 2/2 (+100.004µs): return nil
                                            Task#270 ends at 394.483µs
                                              Combine#270:
                                              Combine#270 step 1/4 (+0s): 2.296µs self time
                                              Combine#270 step 2/4 (+2.296µs): scatter:
                                                Task#268: pool=1
                                                Task#268 step 1/2 (+0s): 66.296938ms self time
                                                Task#268 step 2/2 (+66.296938ms): return nil
                                                Task#268 ends at 66.693717ms
                                                  Gather#268:
                                                  Gather#268 step 1/2 (+0s): 8.52µs self time
                                                  Gather#268 step 2/2 (+8.52µs): return nil
                                                  Gather#268 ends at 66.702237ms
                                              Combine#270 step 3/4 (+2.296µs): 7.688µs self time
                                              Combine#270 step 4/4 (+9.984µs): return nil
                                              Combine#270 ends at 404.467µs
                                          Gather#303 step 3/6 (+1.766µs): 1.791µs self time
                                          Gather#303 step 4/6 (+3.557µs): scatter:
                                            Task#221: pool=0
                                            Task#221 step 1/2 (+0s): 99.982µs self time
                                            Task#221 step 2/2 (+99.982µs): return nil
                                            Task#221 ends at 396.252µs
                                              Gather#221:
                                              Gather#221 step 1/2 (+0s): 9.774µs self time
                                              Gather#221 step 2/2 (+9.774µs): return nil
                                              Gather#221 ends at 406.026µs
                                          Gather#303 step 5/6 (+3.557µs): 1.795µs self time
                                          Gather#303 step 6/6 (+5.352µs): return nil
                                          Gather#303 ends at 298.065µs
                                      Gather#305 step 3/8 (+10.013µs): 0s self time
                                      Gather#305 step 4/8 (+10.013µs): scatter:
                                        Task#271: pool=0
                                        Task#271 step 1/2 (+0s): 99.996µs self time
                                        Task#271 step 2/2 (+99.996µs): return nil
                                        Task#271 ends at 336.402µs
                                          Combine#271:
                                          Combine#271 step 1/6 (+0s): 3.285µs self time
                                          Combine#271 step 2/6 (+3.285µs): subjob:
                                            Plan#19: pathCount=18 taskCount=31 maxPathDuration=61.322025ms
                                            Plan#19 step 1/4 (+0s): scatter:
                                              Task#301: pool=1
                                              Task#301 step 1/2 (+0s): 99.766µs self time
                                              Task#301 step 2/2 (+99.766µs): return nil
                                              Task#301 ends at 99.766µs
                                                Gather#301:
                                                Gather#301 step 1/8 (+0s): 2.5µs self time
                                                Gather#301 step 2/8 (+2.5µs): scatter:
                                                  Task#274: pool=0
                                                  Task#274 step 1/2 (+0s): 61.209705ms self time
                                                  Task#274 step 2/2 (+61.209705ms): return nil
                                                  Task#274 ends at 61.311971ms
                                                    Combine#274:
                                                    Combine#274 step 1/2 (+0s): 10.054µs self time
                                                    Combine#274 step 2/2 (+10.054µs): return nil
                                                    Combine#274 ends at 61.322025ms
                                                Gather#301 step 3/8 (+2.5µs): 2.327µs self time
                                                Gather#301 step 4/8 (+4.827µs): scatter:
                                                  Task#298: pool=0
                                                  Task#298 step 1/2 (+0s): 96.831µs self time
                                                  Task#298 step 2/2 (+96.831µs): return nil
                                                  Task#298 ends at 201.424µs
                                                    Gather#298:
                                                    Gather#298 step 1/4 (+0s): 1.824µs self time
                                                    Gather#298 step 2/4 (+1.824µs): scatter:
                                                      Task#282: pool=0
                                                      Task#282 step 1/2 (+0s): 100.002µs self time
                                                      Task#282 step 2/2 (+100.002µs): return nil
                                                      Task#282 ends at 303.25µs
                                                        Gather#282:
                                                        Gather#282 step 1/2 (+0s): 7.341µs self time
                                                        Gather#282 step 2/2 (+7.341µs): return nil
                                                        Gather#282 ends at 310.591µs
                                                    Gather#298 step 3/4 (+1.824µs): 7.478µs self time
                                                    Gather#298 step 4/4 (+9.302µs): return nil
                                                    Gather#298 ends at 210.726µs
                                                Gather#301 step 5/8 (+4.827µs): 3.777µs self time
                                                Gather#301 step 6/8 (+8.604µs): scatter:
                                                  Task#299: pool=0
                                                  Task#299 step 1/2 (+0s): 99.503µs self time
                                                  Task#299 step 2/2 (+99.503µs): return nil
                                                  Task#299 ends at 207.873µs
                                                    Gather#299:
                                                    Gather#299 step 1/12 (+0s): 1.66µs self time
                                                    Gather#299 step 2/12 (+1.66µs): scatter:
                                                      Task#283: pool=1
                                                      Task#283 step 1/2 (+0s): 104.058µs self time
                                                      Task#283 step 2/2 (+104.058µs): return nil
                                                      Task#283 ends at 313.591µs
                                                        Combine#283:
                                                        Combine#283 step 1/2 (+0s): 10.021µs self time
                                                        Combine#283 step 2/2 (+10.021µs): return nil
                                                        Combine#283 ends at 323.612µs
                                                    Gather#299 step 3/12 (+1.66µs): 1.676µs self time
                                                    Gather#299 step 4/12 (+3.336µs): scatter:
                                                      Task#277: pool=0
                                                      Task#277 step 1/2 (+0s): 100.169µs self time
                                                      Task#277 step 2/2 (+100.169µs): return nil
                                                      Task#277 ends at 311.378µs
                                                        Gather#277:
                                                        Gather#277 step 1/2 (+0s): 1.076736ms self time
                                                        Gather#277 step 2/2 (+1.076736ms): return nil
                                                        Gather#277 ends at 1.388114ms
                                                    Gather#299 step 5/12 (+3.336µs): 1.681µs self time
                                                    Gather#299 step 6/12 (+5.017µs): scatter:
                                                      Task#296: pool=0
                                                      Task#296 step 1/2 (+0s): 99.998µs self time
                                                      Task#296 step 2/2 (+99.998µs): return nil
                                                      Task#296 ends at 312.888µs
                                                        Gather#296:
                                                        Gather#296 step 1/6 (+0s): 3.284µs self time
                                                        Gather#296 step 2/6 (+3.284µs): scatter:
                                                          Task#273: pool=1
                                                          Task#273 step 1/2 (+0s): 99.952µs self time
                                                          Task#273 step 2/2 (+99.952µs): return nil
                                                          Task#273 ends at 416.124µs
                                                            Gather#273:
                                                            Gather#273 step 1/2 (+0s): 6.789µs self time
                                                            Gather#273 step 2/2 (+6.789µs): return nil
                                                            Gather#273 ends at 422.913µs
                                                        Gather#296 step 3/6 (+3.284µs): 4.486µs self time
                                                        Gather#296 step 4/6 (+7.77µs): scatter:
                                                          Task#286: pool=1
                                                          Task#286 step 1/2 (+0s): 100.006µs self time
                                                          Task#286 step 2/2 (+100.006µs): return nil
                                                          Task#286 ends at 420.664µs
                                                            Gather#286:
                                                            Gather#286 step 1/2 (+0s): 11.43µs self time
                                                            Gather#286 step 2/2 (+11.43µs): return nil
                                                            Gather#286 ends at 432.094µs
                                                        Gather#296 step 5/6 (+7.77µs): 2.274µs self time
                                                        Gather#296 step 6/6 (+10.044µs): return nil
                                                        Gather#296 ends at 322.932µs
                                                    Gather#299 step 7/12 (+5.017µs): 1.138µs self time
                                                    Gather#299 step 8/12 (+6.155µs): scatter:
                                                      Task#287: pool=0
                                                      Task#287 step 1/2 (+0s): 99.996µs self time
                                                      Task#287 step 2/2 (+99.996µs): return nil
                                                      Task#287 ends at 314.024µs
                                                        Gather#287:
                                                        Gather#287 step 1/2 (+0s): 10.002µs self time
                                                        Gather#287 step 2/2 (+10.002µs): return nil
                                                        Gather#287 ends at 324.026µs
                                                    Gather#299 step 9/12 (+6.155µs): 1.945µs self time
                                                    Gather#299 step 10/12 (+8.1µs): scatter:
                                                      Task#295: pool=1
                                                      Task#295 step 1/2 (+0s): 100.069µs self time
                                                      Task#295 step 2/2 (+100.069µs): return nil
                                                      Task#295 ends at 316.042µs
                                                        Gather#295:
                                                        Gather#295 step 1/6 (+0s): 81.092µs self time
                                                        Gather#295 step 2/6 (+81.092µs): scatter:
                                                          Task#293: pool=0
                                                          Task#293 step 1/2 (+0s): 100.003µs self time
                                                          Task#293 step 2/2 (+100.003µs): return error
                                                          Task#293 ends at 497.137µs
                                                            Gather#293:
                                                            Gather#293 step 1/14 (+0s): 812ns self time
                                                            Gather#293 step 2/14 (+812ns): scatter:
                                                              Task#292: pool=1
                                                              Task#292 step 1/2 (+0s): 99.987µs self time
                                                              Task#292 step 2/2 (+99.987µs): return nil
                                                              Task#292 ends at 597.936µs
                                                                Gather#292:
                                                                Gather#292 step 1/6 (+0s): 3.325µs self time
                                                                Gather#292 step 2/6 (+3.325µs): scatter:
                                                                  Task#272: pool=1
                                                                  Task#272 step 1/2 (+0s): 100.269µs self time
                                                                  Task#272 step 2/2 (+100.269µs): return nil
                                                                  Task#272 ends at 701.53µs
                                                                    Gather#272:
                                                                    Gather#272 step 1/2 (+0s): 10.027µs self time
                                                                    Gather#272 step 2/2 (+10.027µs): return nil
                                                                    Gather#272 ends at 711.557µs
                                                                Gather#292 step 3/6 (+3.325µs): 3.315µs self time
                                                                Gather#292 step 4/6 (+6.64µs): scatter:
                                                                  Task#276: pool=0
                                                                  Task#276 step 1/2 (+0s): 99.999µs self time
                                                                  Task#276 step 2/2 (+99.999µs): return nil
                                                                  Task#276 ends at 704.575µs
                                                                    Combine#276:
                                                                    Combine#276 step 1/2 (+0s): 0s self time
                                                                    Combine#276 step 2/2 (+0s): return nil
                                                                    Combine#276 ends at 704.575µs
                                                                Gather#292 step 5/6 (+6.64µs): 3.313µs self time
                                                                Gather#292 step 6/6 (+9.953µs): return nil
                                                                Gather#292 ends at 607.889µs
                                                            Gather#293 step 3/14 (+812ns): 1.541µs self time
                                                            Gather#293 step 4/14 (+2.353µs): scatter:
                                                              Task#278: pool=1
                                                              Task#278 step 1/2 (+0s): 99.505µs self time
                                                              Task#278 step 2/2 (+99.505µs): return nil
                                                              Task#278 ends at 598.995µs
                                                                Gather#278:
                                                                Gather#278 step 1/2 (+0s): 10µs self time
                                                                Gather#278 step 2/2 (+10µs): return nil
                                                                Gather#278 ends at 608.995µs
                                                            Gather#293 step 5/14 (+2.353µs): 1.542µs self time
                                                            Gather#293 step 6/14 (+3.895µs): scatter:
                                                              Task#279: pool=1
                                                              Task#279 step 1/2 (+0s): 1.407213ms self time
                                                              Task#279 step 2/2 (+1.407213ms): return nil
                                                              Task#279 ends at 1.908245ms
                                                                Gather#279:
                                                                Gather#279 step 1/2 (+0s): 11.012µs self time
                                                                Gather#279 step 2/2 (+11.012µs): return nil
                                                                Gather#279 ends at 1.919257ms
                                                            Gather#293 step 7/14 (+3.895µs): 1.54µs self time
                                                            Gather#293 step 8/14 (+5.435µs): scatter:
                                                              Task#275: pool=1
                                                              Task#275 step 1/2 (+0s): 96.415µs self time
                                                              Task#275 step 2/2 (+96.415µs): return nil
                                                              Task#275 ends at 598.987µs
                                                                Gather#275:
                                                                Gather#275 step 1/2 (+0s): 10.001µs self time
                                                                Gather#275 step 2/2 (+10.001µs): return nil
                                                                Gather#275 ends at 608.988µs
                                                            Gather#293 step 9/14 (+5.435µs): 1.539µs self time
                                                            Gather#293 step 10/14 (+6.974µs): scatter:
                                                              Task#290: pool=1
                                                              Task#290 step 1/2 (+0s): 100.034µs self time
                                                              Task#290 step 2/2 (+100.034µs): return nil
                                                              Task#290 ends at 604.145µs
                                                                Gather#290:
                                                                Gather#290 step 1/4 (+0s): 5.024µs self time
                                                                Gather#290 step 2/4 (+5.024µs): scatter:
                                                                  Task#285: pool=0
                                                                  Task#285 step 1/2 (+0s): 100.001µs self time
                                                                  Task#285 step 2/2 (+100.001µs): return nil
                                                                  Task#285 ends at 709.17µs
                                                                    Gather#285:
                                                                    Gather#285 step 1/2 (+0s): 9.975µs self time
                                                                    Gather#285 step 2/2 (+9.975µs): return nil
                                                                    Gather#285 ends at 719.145µs
                                                                Gather#290 step 3/4 (+5.024µs): 5.024µs self time
                                                                Gather#290 step 4/4 (+10.048µs): return nil
                                                                Gather#290 ends at 614.193µs
                                                            Gather#293 step 11/14 (+6.974µs): 1.752µs self time
                                                            Gather#293 step 12/14 (+8.726µs): scatter:
                                                              Task#291: pool=1
                                                              Task#291 step 1/2 (+0s): 99.969µs self time
                                                              Task#291 step 2/2 (+99.969µs): return nil
                                                              Task#291 ends at 605.832µs
                                                                Gather#291:
                                                                Gather#291 step 1/4 (+0s): 3.527µs self time
                                                                Gather#291 step 2/4 (+3.527µs): scatter:
                                                                  Task#288: pool=1
                                                                  Task#288 step 1/2 (+0s): 215.598µs self time
                                                                  Task#288 step 2/2 (+215.598µs): return nil
                                                                  Task#288 ends at 824.957µs
                                                                    Gather#288:
                                                                    Gather#288 step 1/2 (+0s): 1.169998ms self time
                                                                    Gather#288 step 2/2 (+1.169998ms): return nil
                                                                    Gather#288 ends at 1.994955ms
                                                                Gather#291 step 3/4 (+3.527µs): 3.298µs self time
                                                                Gather#291 step 4/4 (+6.825µs): return nil
                                                                Gather#291 ends at 612.657µs
                                                            Gather#293 step 13/14 (+8.726µs): 1.33µs self time
                                                            Gather#293 step 14/14 (+10.056µs): return nil
                                                            Gather#293 ends at 507.193µs
                                                        Gather#295 step 3/6 (+81.092µs): 10.791µs self time
                                                        Gather#295 step 4/6 (+91.883µs): scatter:
                                                          Task#289: pool=0
                                                          Task#289 step 1/2 (+0s): 100.003µs self time
                                                          Task#289 step 2/2 (+100.003µs): return nil
                                                          Task#289 ends at 507.928µs
                                                            Gather#289:
                                                            Gather#289 step 1/2 (+0s): 9.848µs self time
                                                            Gather#289 step 2/2 (+9.848µs): return nil
                                                            Gather#289 ends at 517.776µs
                                                        Gather#295 step 5/6 (+91.883µs): 128.08µs self time
                                                        Gather#295 step 6/6 (+219.963µs): return nil
                                                        Gather#295 ends at 536.005µs
                                                    Gather#299 step 11/12 (+8.1µs): 1.947µs self time
                                                    Gather#299 step 12/12 (+10.047µs): return nil
                                                    Gather#299 ends at 217.92µs
                                                Gather#301 step 7/8 (+8.604µs): 1.382µs self time
                                                Gather#301 step 8/8 (+9.986µs): return nil
                                                Gather#301 ends at 109.752µs
                                            Plan#19 step 2/4 (+0s): scatter:
                                              Task#300: pool=0
                                              Task#300 step 1/2 (+0s): 100.239µs self time
                                              Task#300 step 2/2 (+100.239µs): return error
                                              Task#300 ends at 100.239µs
                                                Gather#300:
                                                Gather#300 step 1/4 (+0s): 859ns self time
                                                Gather#300 step 2/4 (+859ns): scatter:
                                                  Task#297: pool=1
                                                  Task#297 step 1/2 (+0s): 22.223µs self time
                                                  Task#297 step 2/2 (+22.223µs): return nil
                                                  Task#297 ends at 123.321µs
                                                    Gather#297:
                                                    Gather#297 step 1/4 (+0s): 5.005µs self time
                                                    Gather#297 step 2/4 (+5.005µs): scatter:
                                                      Task#294: pool=1
                                                      Task#294 step 1/2 (+0s): 100.012µs self time
                                                      Task#294 step 2/2 (+100.012µs): return nil
                                                      Task#294 ends at 228.338µs
                                                        Gather#294:
                                                        Gather#294 step 1/6 (+0s): 56.621µs self time
                                                        Gather#294 step 2/6 (+56.621µs): scatter:
                                                          Task#284: pool=0
                                                          Task#284 step 1/2 (+0s): 135.863µs self time
                                                          Task#284 step 2/2 (+135.863µs): return nil
                                                          Task#284 ends at 420.822µs
                                                            Gather#284:
                                                            Gather#284 step 1/2 (+0s): 10.004µs self time
                                                            Gather#284 step 2/2 (+10.004µs): return nil
                                                            Gather#284 ends at 430.826µs
                                                        Gather#294 step 3/6 (+56.621µs): 56.15µs self time
                                                        Gather#294 step 4/6 (+112.771µs): scatter:
                                                          Task#281: pool=0
                                                          Task#281 step 1/2 (+0s): 211.863µs self time
                                                          Task#281 step 2/2 (+211.863µs): return nil
                                                          Task#281 ends at 552.972µs
                                                            Gather#281:
                                                            Gather#281 step 1/2 (+0s): 9.998µs self time
                                                            Gather#281 step 2/2 (+9.998µs): return nil
                                                            Gather#281 ends at 562.97µs
                                                        Gather#294 step 5/6 (+112.771µs): 56.113µs self time
                                                        Gather#294 step 6/6 (+168.884µs): return nil
                                                        Gather#294 ends at 397.222µs
                                                    Gather#297 step 3/4 (+5.005µs): 5.004µs self time
                                                    Gather#297 step 4/4 (+10.009µs): return nil
                                                    Gather#297 ends at 133.33µs
                                                Gather#300 step 3/4 (+859ns): 982ns self time
                                                Gather#300 step 4/4 (+1.841µs): return nil
                                                Gather#300 ends at 102.08µs
                                            Plan#19 step 3/4 (+0s): scatter:
                                              Task#302: pool=0
                                              Task#302 step 1/2 (+0s): 100.001µs self time
                                              Task#302 step 2/2 (+100.001µs): return nil
                                              Task#302 ends at 100.001µs
                                                Gather#302:
                                                Gather#302 step 1/4 (+0s): 2.99836ms self time
                                                Gather#302 step 2/4 (+2.99836ms): scatter:
                                                  Task#280: pool=0
                                                  Task#280 step 1/2 (+0s): 101.794µs self time
                                                  Task#280 step 2/2 (+101.794µs): return nil
                                                  Task#280 ends at 3.200155ms
                                                    Gather#280:
                                                    Gather#280 step 1/2 (+0s): 10.276µs self time
                                                    Gather#280 step 2/2 (+10.276µs): return nil
                                                    Gather#280 ends at 3.210431ms
                                                Gather#302 step 3/4 (+2.99836ms): 2.998367ms self time
                                                Gather#302 step 4/4 (+5.996727ms): return nil
                                                Gather#302 ends at 6.096728ms
                                            Plan#19 step 4/4 (+0s): ends at 61.322025ms
                                          Combine#271 step 3/6 (+61.32531ms): 5.494µs self time
                                          Combine#271 step 4/6 (+61.330804ms): scatter:
                                            Task#269: pool=1
                                            Task#269 step 1/2 (+0s): 99.999µs self time
                                            Task#269 step 2/2 (+99.999µs): return error
                                            Task#269 ends at 61.767205ms
                                              Gather#269:
                                              Gather#269 step 1/4 (+0s): 0s self time
                                              Gather#269 step 2/4 (+0s): scatter:
                                                Task#263: pool=1
                                                Task#263 step 1/2 (+0s): 100.024µs self time
                                                Task#263 step 2/2 (+100.024µs): return error
                                                Task#263 ends at 61.867229ms
                                                  Gather#263:
                                                  Gather#263 step 1/2 (+0s): 9.997µs self time
                                                  Gather#263 step 2/2 (+9.997µs): return nil
                                                  Gather#263 ends at 61.877226ms
                                              Gather#269 step 3/4 (+0s): 9.973µs self time
                                              Gather#269 step 4/4 (+9.973µs): return nil
                                              Gather#269 ends at 61.777178ms
                                          Combine#271 step 5/6 (+61.330804ms): 1.077µs self time
                                          Combine#271 step 6/6 (+61.331881ms): return nil
                                          Combine#271 ends at 61.668283ms
                                      Gather#305 step 5/8 (+10.013µs): 0s self time
                                      Gather#305 step 6/8 (+10.013µs): scatter:
                                        Task#304: pool=1
                                        Task#304 step 1/2 (+0s): 99.955µs self time
                                        Task#304 step 2/2 (+99.955µs): return nil
                                        Task#304 ends at 336.361µs
                                          Gather#304:
                                          Gather#304 step 1/4 (+0s): 2.600414ms self time
                                          Gather#304 step 2/4 (+2.600414ms): scatter:
                                            Task#265: pool=0
                                            Task#265 step 1/2 (+0s): 100ms self time
                                            Task#265 step 2/2 (+100ms): return nil
                                            Task#265 ends at 102.936775ms
                                              Gather#265:
                                              Gather#265 step 1/2 (+0s): 10.003µs self time
                                              Gather#265 step 2/2 (+10.003µs): return nil
                                              Gather#265 ends at 102.946778ms
                                          Gather#304 step 3/4 (+2.600414ms): 2.808802ms self time
                                          Gather#304 step 4/4 (+5.409216ms): return nil
                                          Gather#304 ends at 5.745577ms
                                      Gather#305 step 7/8 (+10.013µs): 0s self time
                                      Gather#305 step 8/8 (+10.013µs): return nil
                                      Gather#305 ends at 236.406µs
                                  Combine#306 step 5/14 (+5.15µs): 2.737µs self time
                                  Combine#306 step 6/14 (+7.887µs): scatter:
                                    Task#267: pool=0
                                    Task#267 step 1/2 (+0s): 4.594914ms self time
                                    Task#267 step 2/2 (+4.594914ms): return nil
                                    Task#267 ends at 4.722461ms
                                      Gather#267:
                                      Gather#267 step 1/2 (+0s): 9.997µs self time
                                      Gather#267 step 2/2 (+9.997µs): return nil
                                      Gather#267 ends at 4.732458ms
                                  Combine#306 step 7/14 (+7.887µs): 2.536µs self time
                                  Combine#306 step 8/14 (+10.423µs): scatter:
                                    Task#266: pool=0
                                    Task#266 step 1/2 (+0s): 100.003µs self time
                                    Task#266 step 2/2 (+100.003µs): return nil
                                    Task#266 ends at 230.086µs
                                      Gather#266:
                                      Gather#266 step 1/2 (+0s): 2.520772ms self time
                                      Gather#266 step 2/2 (+2.520772ms): return nil
                                      Gather#266 ends at 2.750858ms
                                  Combine#306 step 9/14 (+10.423µs): 2.507µs self time
                                  Combine#306 step 10/14 (+12.93µs): scatter:
                                    Task#264: pool=1
                                    Task#264 step 1/2 (+0s): 99.998µs self time
                                    Task#264 step 2/2 (+99.998µs): return nil
                                    Task#264 ends at 232.588µs
                                      Gather#264:
                                      Gather#264 step 1/2 (+0s): 15.666µs self time
                                      Gather#264 step 2/2 (+15.666µs): return nil
                                      Gather#264 ends at 248.254µs
                                  Combine#306 step 11/14 (+12.93µs): 2.052µs self time
                                  Combine#306 step 12/14 (+14.982µs): scatter:
                                    Task#223: pool=1
                                    Task#223 step 1/4 (+0s): 26.829µs self time
                                    Task#223 step 2/4 (+26.829µs): subjob:
                                      Plan#18: pathCount=19 taskCount=38 maxPathDuration=100.115168ms
                                      Plan#18 step 1/7 (+0s): scatter:
                                        Task#259: pool=0
                                        Task#259 step 1/2 (+0s): 98.573µs self time
                                        Task#259 step 2/2 (+98.573µs): return nil
                                        Task#259 ends at 98.573µs
                                          Gather#259:
                                          Gather#259 step 1/6 (+0s): 3.331µs self time
                                          Gather#259 step 2/6 (+3.331µs): scatter:
                                            Task#253: pool=0
                                            Task#253 step 1/2 (+0s): 99.135µs self time
                                            Task#253 step 2/2 (+99.135µs): return nil
                                            Task#253 ends at 201.039µs
                                              Gather#253:
                                              Gather#253 step 1/4 (+0s): 4.192µs self time
                                              Gather#253 step 2/4 (+4.192µs): scatter:
                                                Task#230: pool=0
                                                Task#230 step 1/2 (+0s): 22.397µs self time
                                                Task#230 step 2/2 (+22.397µs): return nil
                                                Task#230 ends at 227.628µs
                                                  Combine#230:
                                                  Combine#230 step 1/2 (+0s): 523.583µs self time
                                                  Combine#230 step 2/2 (+523.583µs): return nil
                                                  Combine#230 ends at 751.211µs
                                              Gather#253 step 3/4 (+4.192µs): 4.207µs self time
                                              Gather#253 step 4/4 (+8.399µs): return nil
                                              Gather#253 ends at 209.438µs
                                          Gather#259 step 3/6 (+3.331µs): 3.334µs self time
                                          Gather#259 step 4/6 (+6.665µs): scatter:
                                            Task#241: pool=0
                                            Task#241 step 1/2 (+0s): 99.995µs self time
                                            Task#241 step 2/2 (+99.995µs): return nil
                                            Task#241 ends at 205.233µs
                                              Gather#241:
                                              Gather#241 step 1/2 (+0s): 6.755379ms self time
                                              Gather#241 step 2/2 (+6.755379ms): return nil
                                              Gather#241 ends at 6.960612ms
                                          Gather#259 step 5/6 (+6.665µs): 3.336µs self time
                                          Gather#259 step 6/6 (+10.001µs): return nil
                                          Gather#259 ends at 108.574µs
                                      Plan#18 step 2/7 (+0s): scatter:
                                        Task#261: pool=0
                                        Task#261 step 1/2 (+0s): 99.987µs self time
                                        Task#261 step 2/2 (+99.987µs): return nil
                                        Task#261 ends at 99.987µs
                                          Gather#261:
                                          Gather#261 step 1/6 (+0s): 3.578µs self time
                                          Gather#261 step 2/6 (+3.578µs): scatter:
                                            Task#233: pool=0
                                            Task#233 step 1/2 (+0s): 99.999µs self time
                                            Task#233 step 2/2 (+99.999µs): return nil
                                            Task#233 ends at 203.564µs
                                              Gather#233:
                                              Gather#233 step 1/2 (+0s): 8.295µs self time
                                              Gather#233 step 2/2 (+8.295µs): return nil
                                              Gather#233 ends at 211.859µs
                                          Gather#261 step 3/6 (+3.578µs): 3.557µs self time
                                          Gather#261 step 4/6 (+7.135µs): scatter:
                                            Task#255: pool=0
                                            Task#255 step 1/2 (+0s): 59.947µs self time
                                            Task#255 step 2/2 (+59.947µs): return nil
                                            Task#255 ends at 167.069µs
                                              Gather#255:
                                              Gather#255 step 1/4 (+0s): 1.42687ms self time
                                              Gather#255 step 2/4 (+1.42687ms): scatter:
                                                Task#251: pool=0
                                                Task#251 step 1/2 (+0s): 38.769612ms self time
                                                Task#251 step 2/2 (+38.769612ms): return nil
                                                Task#251 ends at 40.363551ms
                                                  Gather#251:
                                                  Gather#251 step 1/4 (+0s): 4.969µs self time
                                                  Gather#251 step 2/4 (+4.969µs): scatter:
                                                    Task#244: pool=0
                                                    Task#244 step 1/2 (+0s): 108.184µs self time
                                                    Task#244 step 2/2 (+108.184µs): return nil
                                                    Task#244 ends at 40.476704ms
                                                      Gather#244:
                                                      Gather#244 step 1/6 (+0s): 7.5µs self time
                                                      Gather#244 step 2/6 (+7.5µs): scatter:
                                                        Task#237: pool=0
                                                        Task#237 step 1/2 (+0s): 92.766µs self time
                                                        Task#237 step 2/2 (+92.766µs): return nil
                                                        Task#237 ends at 40.57697ms
                                                          Gather#237:
                                                          Gather#237 step 1/2 (+0s): 10.019µs self time
                                                          Gather#237 step 2/2 (+10.019µs): return error
                                                          Gather#237 ends at 40.586989ms
                                                      Gather#244 step 3/6 (+7.5µs): 1.266µs self time
                                                      Gather#244 step 4/6 (+8.766µs): scatter:
                                                        Task#236: pool=0
                                                        Task#236 step 1/2 (+0s): 1.833858ms self time
                                                        Task#236 step 2/2 (+1.833858ms): return nil
                                                        Task#236 ends at 42.319328ms
                                                          Combine#236:
                                                          Combine#236 step 1/2 (+0s): 9.998µs self time
                                                          Combine#236 step 2/2 (+9.998µs): return nil
                                                          Combine#236 ends at 42.329326ms
                                                      Gather#244 step 5/6 (+8.766µs): 1.232µs self time
                                                      Gather#244 step 6/6 (+9.998µs): return nil
                                                      Gather#244 ends at 40.486702ms
                                                  Gather#251 step 3/4 (+4.969µs): 4.971µs self time
                                                  Gather#251 step 4/4 (+9.94µs): return nil
                                                  Gather#251 ends at 40.373491ms
                                              Gather#255 step 3/4 (+1.42687ms): 1.426874ms self time
                                              Gather#255 step 4/4 (+2.853744ms): return error
                                              Gather#255 ends at 3.020813ms
                                          Gather#261 step 5/6 (+7.135µs): 3.566µs self time
                                          Gather#261 step 6/6 (+10.701µs): return nil
                                          Gather#261 ends at 110.688µs
                                      Plan#18 step 3/7 (+0s): scatter:
                                        Task#258: pool=0
                                        Task#258 step 1/2 (+0s): 100ms self time
                                        Task#258 step 2/2 (+100ms): return nil
                                        Task#258 ends at 100ms
                                          Gather#258:
                                          Gather#258 step 1/8 (+0s): 2.518µs self time
                                          Gather#258 step 2/8 (+2.518µs): scatter:
                                            Task#224: pool=0
                                            Task#224 step 1/2 (+0s): 67.562µs self time
                                            Task#224 step 2/2 (+67.562µs): return nil
                                            Task#224 ends at 100.07008ms
                                              Gather#224:
                                              Gather#224 step 1/2 (+0s): 10.141µs self time
                                              Gather#224 step 2/2 (+10.141µs): return nil
                                              Gather#224 ends at 100.080221ms
                                          Gather#258 step 3/8 (+2.518µs): 2.658µs self time
                                          Gather#258 step 4/8 (+5.176µs): scatter:
                                            Task#239: pool=0
                                            Task#239 step 1/2 (+0s): 100.451µs self time
                                            Task#239 step 2/2 (+100.451µs): return nil
                                            Task#239 ends at 100.105627ms
                                              Gather#239:
                                              Gather#239 step 1/2 (+0s): 9.541µs self time
                                              Gather#239 step 2/2 (+9.541µs): return nil
                                              Gather#239 ends at 100.115168ms
                                          Gather#258 step 5/8 (+5.176µs): 2.332µs self time
                                          Gather#258 step 6/8 (+7.508µs): scatter:
                                            Task#234: pool=0
                                            Task#234 step 1/2 (+0s): 100.009µs self time
                                            Task#234 step 2/2 (+100.009µs): return nil
                                            Task#234 ends at 100.107517ms
                                              Gather#234:
                                              Gather#234 step 1/2 (+0s): 247ns self time
                                              Gather#234 step 2/2 (+247ns): return nil
                                              Gather#234 ends at 100.107764ms
                                          Gather#258 step 7/8 (+7.508µs): 2.473µs self time
                                          Gather#258 step 8/8 (+9.981µs): return nil
                                          Gather#258 ends at 100.009981ms
                                      Plan#18 step 4/7 (+0s): scatter:
                                        Task#256: pool=0
                                        Task#256 step 1/2 (+0s): 100.006µs self time
                                        Task#256 step 2/2 (+100.006µs): return nil
                                        Task#256 ends at 100.006µs
                                          Gather#256:
                                          Gather#256 step 1/4 (+0s): 5.157µs self time
                                          Gather#256 step 2/4 (+5.157µs): scatter:
                                            Task#254: pool=0
                                            Task#254 step 1/2 (+0s): 3.200047ms self time
                                            Task#254 step 2/2 (+3.200047ms): return nil
                                            Task#254 ends at 3.30521ms
                                              Gather#254:
                                              Gather#254 step 1/8 (+0s): 2.455µs self time
                                              Gather#254 step 2/8 (+2.455µs): scatter:
                                                Task#231: pool=0
                                                Task#231 step 1/2 (+0s): 99.838µs self time
                                                Task#231 step 2/2 (+99.838µs): return nil
                                                Task#231 ends at 3.407503ms
                                                  Gather#231:
                                                  Gather#231 step 1/2 (+0s): 3.605154ms self time
                                                  Gather#231 step 2/2 (+3.605154ms): return nil
                                                  Gather#231 ends at 7.012657ms
                                              Gather#254 step 3/8 (+2.455µs): 2.52µs self time
                                              Gather#254 step 4/8 (+4.975µs): scatter:
                                                Task#247: pool=0
                                                Task#247 step 1/2 (+0s): 1.648733ms self time
                                                Task#247 step 2/2 (+1.648733ms): return nil
                                                Task#247 ends at 4.958918ms
                                                  Combine#247:
                                                  Combine#247 step 1/4 (+0s): 5.85µs self time
                                                  Combine#247 step 2/4 (+5.85µs): scatter:
                                                    Task#245: pool=0
                                                    Task#245 step 1/2 (+0s): 2.141872ms self time
                                                    Task#245 step 2/2 (+2.141872ms): return nil
                                                    Task#245 ends at 7.10664ms
                                                      Gather#245:
                                                      Gather#245 step 1/4 (+0s): 2.427µs self time
                                                      Gather#245 step 2/4 (+2.427µs): scatter:
                                                        Task#243: pool=0
                                                        Task#243 step 1/2 (+0s): 100.064µs self time
                                                        Task#243 step 2/2 (+100.064µs): return nil
                                                        Task#243 ends at 7.209131ms
                                                          Gather#243:
                                                          Gather#243 step 1/6 (+0s): 934ns self time
                                                          Gather#243 step 2/6 (+934ns): scatter:
                                                            Task#238: pool=0
                                                            Task#238 step 1/2 (+0s): 98.676µs self time
                                                            Task#238 step 2/2 (+98.676µs): return nil
                                                            Task#238 ends at 7.308741ms
                                                              Gather#238:
                                                              Gather#238 step 1/2 (+0s): 25.919µs self time
                                                              Gather#238 step 2/2 (+25.919µs): return nil
                                                              Gather#238 ends at 7.33466ms
                                                          Gather#243 step 3/6 (+934ns): 359ns self time
                                                          Gather#243 step 4/6 (+1.293µs): scatter:
                                                            Task#242: pool=0
                                                            Task#242 step 1/2 (+0s): 100µs self time
                                                            Task#242 step 2/2 (+100µs): return nil
                                                            Task#242 ends at 7.310424ms
                                                              Gather#242:
                                                              Gather#242 step 1/2 (+0s): 11.838µs self time
                                                              Gather#242 step 2/2 (+11.838µs): return nil
                                                              Gather#242 ends at 7.322262ms
                                                          Gather#243 step 5/6 (+1.293µs): 346ns self time
                                                          Gather#243 step 6/6 (+1.639µs): return error
                                                          Gather#243 ends at 7.21077ms
                                                      Gather#245 step 3/4 (+2.427µs): 7.577µs self time
                                                      Gather#245 step 4/4 (+10.004µs): return nil
                                                      Gather#245 ends at 7.116644ms
                                                  Combine#247 step 3/4 (+5.85µs): 4.081µs self time
                                                  Combine#247 step 4/4 (+9.931µs): return nil
                                                  Combine#247 ends at 4.968849ms
                                              Gather#254 step 5/8 (+4.975µs): 2.534µs self time
                                              Gather#254 step 6/8 (+7.509µs): scatter:
                                                Task#250: pool=0
                                                Task#250 step 1/2 (+0s): 99.996µs self time
                                                Task#250 step 2/2 (+99.996µs): return nil
                                                Task#250 ends at 3.412715ms
                                                  Gather#250:
                                                  Gather#250 step 1/6 (+0s): 1.057µs self time
                                                  Gather#250 step 2/6 (+1.057µs): scatter:
                                                    Task#228: pool=0
                                                    Task#228 step 1/2 (+0s): 99.993µs self time
                                                    Task#228 step 2/2 (+99.993µs): return nil
                                                    Task#228 ends at 3.513765ms
                                                      Gather#228:
                                                      Gather#228 step 1/2 (+0s): 10.011µs self time
                                                      Gather#228 step 2/2 (+10.011µs): return nil
                                                      Gather#228 ends at 3.523776ms
                                                  Gather#250 step 3/6 (+1.057µs): 4.471µs self time
                                                  Gather#250 step 4/6 (+5.528µs): scatter:
                                                    Task#232: pool=0
                                                    Task#232 step 1/2 (+0s): 100.079µs self time
                                                    Task#232 step 2/2 (+100.079µs): return nil
                                                    Task#232 ends at 3.518322ms
                                                      Gather#232:
                                                      Gather#232 step 1/2 (+0s): 9.997µs self time
                                                      Gather#232 step 2/2 (+9.997µs): return nil
                                                      Gather#232 ends at 3.528319ms
                                                  Gather#250 step 5/6 (+5.528µs): 4.475µs self time
                                                  Gather#250 step 6/6 (+10.003µs): return nil
                                                  Gather#250 ends at 3.422718ms
                                              Gather#254 step 7/8 (+7.509µs): 2.487µs self time
                                              Gather#254 step 8/8 (+9.996µs): return nil
                                              Gather#254 ends at 3.315206ms
                                          Gather#256 step 3/4 (+5.157µs): 5.065µs self time
                                          Gather#256 step 4/4 (+10.222µs): return nil
                                          Gather#256 ends at 110.228µs
                                      Plan#18 step 5/7 (+0s): scatter:
                                        Task#260: pool=0
                                        Task#260 step 1/2 (+0s): 99.994µs self time
                                        Task#260 step 2/2 (+99.994µs): return nil
                                        Task#260 ends at 99.994µs
                                          Gather#260:
                                          Gather#260 step 1/4 (+0s): 4.977µs self time
                                          Gather#260 step 2/4 (+4.977µs): scatter:
                                            Task#252: pool=0
                                            Task#252 step 1/2 (+0s): 99.994µs self time
                                            Task#252 step 2/2 (+99.994µs): return nil
                                            Task#252 ends at 204.965µs
                                              Gather#252:
                                              Gather#252 step 1/12 (+0s): 1.19µs self time
                                              Gather#252 step 2/12 (+1.19µs): scatter:
                                                Task#248: pool=0
                                                Task#248 step 1/2 (+0s): 99.999µs self time
                                                Task#248 step 2/2 (+99.999µs): return nil
                                                Task#248 ends at 306.154µs
                                                  Gather#248:
                                                  Gather#248 step 1/4 (+0s): 5.001µs self time
                                                  Gather#248 step 2/4 (+5.001µs): scatter:
                                                    Task#226: pool=0
                                                    Task#226 step 1/2 (+0s): 98.622µs self time
                                                    Task#226 step 2/2 (+98.622µs): return nil
                                                    Task#226 ends at 409.777µs
                                                      Gather#226:
                                                      Gather#226 step 1/2 (+0s): 9.989µs self time
                                                      Gather#226 step 2/2 (+9.989µs): return nil
                                                      Gather#226 ends at 419.766µs
                                                  Gather#248 step 3/4 (+5.001µs): 4.999µs self time
                                                  Gather#248 step 4/4 (+10µs): return nil
                                                  Gather#248 ends at 316.154µs
                                              Gather#252 step 3/12 (+1.19µs): 1.76µs self time
                                              Gather#252 step 4/12 (+2.95µs): scatter:
                                                Task#227: pool=0
                                                Task#227 step 1/2 (+0s): 7.563179ms self time
                                                Task#227 step 2/2 (+7.563179ms): return nil
                                                Task#227 ends at 7.771094ms
                                                  Gather#227:
                                                  Gather#227 step 1/2 (+0s): 10µs self time
                                                  Gather#227 step 2/2 (+10µs): return error
                                                  Gather#227 ends at 7.781094ms
                                              Gather#252 step 5/12 (+2.95µs): 2.808µs self time
                                              Gather#252 step 6/12 (+5.758µs): scatter:
                                                Task#229: pool=0
                                                Task#229 step 1/2 (+0s): 100.036µs self time
                                                Task#229 step 2/2 (+100.036µs): return nil
                                                Task#229 ends at 310.759µs
                                                  Gather#229:
                                                  Gather#229 step 1/2 (+0s): 8.905µs self time
                                                  Gather#229 step 2/2 (+8.905µs): return nil
                                                  Gather#229 ends at 319.664µs
                                              Gather#252 step 7/12 (+5.758µs): 1.417µs self time
                                              Gather#252 step 8/12 (+7.175µs): scatter:
                                                Task#225: pool=0
                                                Task#225 step 1/2 (+0s): 99.988µs self time
                                                Task#225 step 2/2 (+99.988µs): return nil
                                                Task#225 ends at 312.128µs
                                                  Gather#225:
                                                  Gather#225 step 1/2 (+0s): 10.005µs self time
                                                  Gather#225 step 2/2 (+10.005µs): return nil
                                                  Gather#225 ends at 322.133µs
                                              Gather#252 step 9/12 (+7.175µs): 2.614µs self time
                                              Gather#252 step 10/12 (+9.789µs): scatter:
                                                Task#249: pool=0
                                                Task#249 step 1/2 (+0s): 102.121µs self time
                                                Task#249 step 2/2 (+102.121µs): return nil
                                                Task#249 ends at 316.875µs
                                                  Gather#249:
                                                  Gather#249 step 1/4 (+0s): 5.976µs self time
                                                  Gather#249 step 2/4 (+5.976µs): scatter:
                                                    Task#246: pool=0
                                                    Task#246 step 1/2 (+0s): 100.148µs self time
                                                    Task#246 step 2/2 (+100.148µs): return nil
                                                    Task#246 ends at 422.999µs
                                                      Gather#246:
                                                      Gather#246 step 1/4 (+0s): 4.927µs self time
                                                      Gather#246 step 2/4 (+4.927µs): scatter:
                                                        Task#235: pool=0
                                                        Task#235 step 1/2 (+0s): 100.002µs self time
                                                        Task#235 step 2/2 (+100.002µs): return nil
                                                        Task#235 ends at 527.928µs
                                                          Gather#235:
                                                          Gather#235 step 1/2 (+0s): 10.005µs self time
                                                          Gather#235 step 2/2 (+10.005µs): return nil
                                                          Gather#235 ends at 537.933µs
                                                      Gather#246 step 3/4 (+4.927µs): 5.014µs self time
                                                      Gather#246 step 4/4 (+9.941µs): return nil
                                                      Gather#246 ends at 432.94µs
                                                  Gather#249 step 3/4 (+5.976µs): 4.01µs self time
                                                  Gather#249 step 4/4 (+9.986µs): return nil
                                                  Gather#249 ends at 326.861µs
                                              Gather#252 step 11/12 (+9.789µs): 214ns self time
                                              Gather#252 step 12/12 (+10.003µs): return nil
                                              Gather#252 ends at 214.968µs
                                          Gather#260 step 3/4 (+4.977µs): 4.982µs self time
                                          Gather#260 step 4/4 (+9.959µs): return nil
                                          Gather#260 ends at 109.953µs
                                      Plan#18 step 6/7 (+0s): scatter:
                                        Task#257: pool=0
                                        Task#257 step 1/2 (+0s): 100.245µs self time
                                        Task#257 step 2/2 (+100.245µs): return nil
                                        Task#257 ends at 100.245µs
                                          Gather#257:
                                          Gather#257 step 1/4 (+0s): 7.448537ms self time
                                          Gather#257 step 2/4 (+7.448537ms): scatter:
                                            Task#240: pool=0
                                            Task#240 step 1/2 (+0s): 99.999µs self time
                                            Task#240 step 2/2 (+99.999µs): return error
                                            Task#240 ends at 7.648781ms
                                              Gather#240:
                                              Gather#240 step 1/2 (+0s): 246.218µs self time
                                              Gather#240 step 2/2 (+246.218µs): return error
                                              Gather#240 ends at 7.894999ms
                                          Gather#257 step 3/4 (+7.448537ms): 430.113µs self time
                                          Gather#257 step 4/4 (+7.87865ms): return nil
                                          Gather#257 ends at 7.978895ms
                                      Plan#18 step 7/7 (+0s): ends at 100.115168ms
                                    Task#223 step 3/4 (+100.141997ms): 73.532µs self time
                                    Task#223 step 4/4 (+100.215529ms): return error
                                    Task#223 ends at 100.350171ms
                                      Gather#223:
                                      Gather#223 step 1/2 (+0s): 42.214µs self time
                                      Gather#223 step 2/2 (+42.214µs): return nil
                                      Gather#223 ends at 100.392385ms
                                  Combine#306 step 13/14 (+14.982µs): 3.053µs self time
                                  Combine#306 step 14/14 (+18.035µs): return nil
                                  Combine#306 ends at 137.695µs
                              Gather#307 step 5/6 (+4.79µs): 2.656µs self time
                              Gather#307 step 6/6 (+7.446µs): return nil
                              Gather#307 ends at 107.434µs
                          Plan#17 step 2/2 (+0s): ends at 102.946778ms
                        Task#195 step 3/4 (+102.966726ms): 80.053µs self time
                        Task#195 step 4/4 (+103.046779ms): return nil
                        Task#195 ends at 104.856323ms
                          Gather#195:
                          Gather#195 step 1/6 (+0s): 3.003µs self time
                          Gather#195 step 2/6 (+3.003µs): subjob:
                            Plan#14: pathCount=5 taskCount=9 maxPathDuration=100.620527ms
                            Plan#14 step 1/2 (+0s): scatter:
                              Task#220: pool=2
                              Task#220 step 1/2 (+0s): 99.892µs self time
                              Task#220 step 2/2 (+99.892µs): return nil
                              Task#220 ends at 99.892µs
                                Gather#220:
                                Gather#220 step 1/10 (+0s): 6.979µs self time
                                Gather#220 step 2/10 (+6.979µs): scatter:
                                  Task#219: pool=5
                                  Task#219 step 1/2 (+0s): 100µs self time
                                  Task#219 step 2/2 (+100µs): return nil
                                  Task#219 ends at 206.871µs
                                    Gather#219:
                                    Gather#219 step 1/4 (+0s): 40.37µs self time
                                    Gather#219 step 2/4 (+40.37µs): scatter:
                                      Task#218: pool=4
                                      Task#218 step 1/2 (+0s): 100.345µs self time
                                      Task#218 step 2/2 (+100.345µs): return nil
                                      Task#218 ends at 347.586µs
                                        Gather#218:
                                        Gather#218 step 1/6 (+0s): 3.299µs self time
                                        Gather#218 step 2/6 (+3.299µs): scatter:
                                          Task#217: pool=2
                                          Task#217 step 1/2 (+0s): 69.305523ms self time
                                          Task#217 step 2/2 (+69.305523ms): return nil
                                          Task#217 ends at 69.656408ms
                                            Gather#217:
                                            Gather#217 step 1/4 (+0s): 3.304µs self time
                                            Gather#217 step 2/4 (+3.304µs): scatter:
                                              Task#207: pool=2
                                              Task#207 step 1/2 (+0s): 100.007µs self time
                                              Task#207 step 2/2 (+100.007µs): return nil
                                              Task#207 ends at 69.759719ms
                                                Gather#207:
                                                Gather#207 step 1/2 (+0s): 4.811µs self time
                                                Gather#207 step 2/2 (+4.811µs): return nil
                                                Gather#207 ends at 69.76453ms
                                            Gather#217 step 3/4 (+3.304µs): 6.725µs self time
                                            Gather#217 step 4/4 (+10.029µs): return nil
                                            Gather#217 ends at 69.666437ms
                                        Gather#218 step 3/6 (+3.299µs): 4.089µs self time
                                        Gather#218 step 4/6 (+7.388µs): scatter:
                                          Task#208: pool=2
                                          Task#208 step 1/4 (+0s): 964ns self time
                                          Task#208 step 2/4 (+964ns): subjob:
                                            Plan#16: pathCount=3 taskCount=8 maxPathDuration=100.204201ms
                                            Plan#16 step 1/3 (+0s): scatter:
                                              Task#215: pool=2
                                              Task#215 step 1/2 (+0s): 100.761µs self time
                                              Task#215 step 2/2 (+100.761µs): return nil
                                              Task#215 ends at 100.761µs
                                                Gather#215:
                                                Gather#215 step 1/4 (+0s): 0s self time
                                                Gather#215 step 2/4 (+0s): scatter:
                                                  Task#214: pool=0
                                                  Task#214 step 1/2 (+0s): 99.341µs self time
                                                  Task#214 step 2/2 (+99.341µs): return nil
                                                  Task#214 ends at 200.102µs
                                                    Gather#214:
                                                    Gather#214 step 1/4 (+0s): 824ns self time
                                                    Gather#214 step 2/4 (+824ns): scatter:
                                                      Task#210: pool=2
                                                      Task#210 step 1/2 (+0s): 100ms self time
                                                      Task#210 step 2/2 (+100ms): return nil
                                                      Task#210 ends at 100.200926ms
                                                        Combine#210:
                                                        Combine#210 step 1/2 (+0s): 3.275µs self time
                                                        Combine#210 step 2/2 (+3.275µs): return error
                                                        Combine#210 ends at 100.204201ms
                                                    Gather#214 step 3/4 (+824ns): 1.55µs self time
                                                    Gather#214 step 4/4 (+2.374µs): return nil
                                                    Gather#214 ends at 202.476µs
                                                Gather#215 step 3/4 (+0s): 0s self time
                                                Gather#215 step 4/4 (+0s): return nil
                                                Gather#215 ends at 100.761µs
                                            Plan#16 step 2/3 (+0s): scatter:
                                              Task#216: pool=1
                                              Task#216 step 1/2 (+0s): 39.188µs self time
                                              Task#216 step 2/2 (+39.188µs): return nil
                                              Task#216 ends at 39.188µs
                                                Gather#216:
                                                Gather#216 step 1/6 (+0s): 708ns self time
                                                Gather#216 step 2/6 (+708ns): scatter:
                                                  Task#213: pool=0
                                                  Task#213 step 1/2 (+0s): 100.249µs self time
                                                  Task#213 step 2/2 (+100.249µs): return nil
                                                  Task#213 ends at 140.145µs
                                                    Gather#213:
                                                    Gather#213 step 1/4 (+0s): 5µs self time
                                                    Gather#213 step 2/4 (+5µs): scatter:
                                                      Task#212: pool=4
                                                      Task#212 step 1/2 (+0s): 99.998µs self time
                                                      Task#212 step 2/2 (+99.998µs): return error
                                                      Task#212 ends at 245.143µs
                                                        Combine#212:
                                                        Combine#212 step 1/4 (+0s): 5.328µs self time
                                                        Combine#212 step 2/4 (+5.328µs): scatter:
                                                          Task#211: pool=2
                                                          Task#211 step 1/2 (+0s): 112.53µs self time
                                                          Task#211 step 2/2 (+112.53µs): return nil
                                                          Task#211 ends at 363.001µs
                                                            Gather#211:
                                                            Gather#211 step 1/2 (+0s): 6.745µs self time
                                                            Gather#211 step 2/2 (+6.745µs): return nil
                                                            Gather#211 ends at 369.746µs
                                                        Combine#212 step 3/4 (+5.328µs): 5.314µs self time
                                                        Combine#212 step 4/4 (+10.642µs): return nil
                                                        Combine#212 ends at 255.785µs
                                                    Gather#213 step 3/4 (+5µs): 5µs self time
                                                    Gather#213 step 4/4 (+10µs): return nil
                                                    Gather#213 ends at 150.145µs
                                                Gather#216 step 3/6 (+708ns): 7.368µs self time
                                                Gather#216 step 4/6 (+8.076µs): scatter:
                                                  Task#209: pool=3
                                                  Task#209 step 1/2 (+0s): 99.962µs self time
                                                  Task#209 step 2/2 (+99.962µs): return nil
                                                  Task#209 ends at 147.226µs
                                                    Gather#209:
                                                    Gather#209 step 1/2 (+0s): 4.209µs self time
                                                    Gather#209 step 2/2 (+4.209µs): return nil
                                                    Gather#209 ends at 151.435µs
                                                Gather#216 step 5/6 (+8.076µs): 1.926µs self time
                                                Gather#216 step 6/6 (+10.002µs): return nil
                                                Gather#216 ends at 49.19µs
                                            Plan#16 step 3/3 (+0s): ends at 100.204201ms
                                          Task#208 step 3/4 (+100.205165ms): 50.382µs self time
                                          Task#208 step 4/4 (+100.255547ms): return nil
                                          Task#208 ends at 100.610521ms
                                            Gather#208:
                                            Gather#208 step 1/2 (+0s): 10.006µs self time
                                            Gather#208 step 2/2 (+10.006µs): return nil
                                            Gather#208 ends at 100.620527ms
                                        Gather#218 step 5/6 (+7.388µs): 2.495µs self time
                                        Gather#218 step 6/6 (+9.883µs): return nil
                                        Gather#218 ends at 357.469µs
                                    Gather#219 step 3/4 (+40.37µs): 40.377µs self time
                                    Gather#219 step 4/4 (+80.747µs): return nil
                                    Gather#219 ends at 287.618µs
                                Gather#220 step 3/10 (+6.979µs): 26.532µs self time
                                Gather#220 step 4/10 (+33.511µs): scatter:
                                  Task#206: pool=5
                                  Task#206 step 1/2 (+0s): 100.015µs self time
                                  Task#206 step 2/2 (+100.015µs): return nil
                                  Task#206 ends at 233.418µs
                                    Gather#206:
                                    Gather#206 step 1/2 (+0s): 10µs self time
                                    Gather#206 step 2/2 (+10µs): return nil
                                    Gather#206 ends at 243.418µs
                                Gather#220 step 5/10 (+33.511µs): 465ns self time
                                Gather#220 step 6/10 (+33.976µs): scatter:
                                  Task#197: pool=0
                                  Task#197 step 1/2 (+0s): 100.128µs self time
                                  Task#197 step 2/2 (+100.128µs): return error
                                  Task#197 ends at 233.996µs
                                    Gather#197:
                                    Gather#197 step 1/4 (+0s): 4.545µs self time
                                    Gather#197 step 2/4 (+4.545µs): subjob:
                                      Plan#15: pathCount=3 taskCount=8 maxPathDuration=24.046628ms
                                      Plan#15 step 1/2 (+0s): scatter:
                                        Task#205: pool=0
                                        Task#205 step 1/2 (+0s): 177.053µs self time
                                        Task#205 step 2/2 (+177.053µs): return nil
                                        Task#205 ends at 177.053µs
                                          Gather#205:
                                          Gather#205 step 1/4 (+0s): 4.998µs self time
                                          Gather#205 step 2/4 (+4.998µs): scatter:
                                            Task#204: pool=0
                                            Task#204 step 1/2 (+0s): 99.992µs self time
                                            Task#204 step 2/2 (+99.992µs): return nil
                                            Task#204 ends at 282.043µs
                                              Gather#204:
                                              Gather#204 step 1/8 (+0s): 2.492µs self time
                                              Gather#204 step 2/8 (+2.492µs): scatter:
                                                Task#203: pool=0
                                                Task#203 step 1/2 (+0s): 23.647126ms self time
                                                Task#203 step 2/2 (+23.647126ms): return nil
                                                Task#203 ends at 23.931661ms
                                                  Gather#203:
                                                  Gather#203 step 1/4 (+0s): 5.018µs self time
                                                  Gather#203 step 2/4 (+5.018µs): scatter:
                                                    Task#198: pool=0
                                                    Task#198 step 1/2 (+0s): 99.996µs self time
                                                    Task#198 step 2/2 (+99.996µs): return nil
                                                    Task#198 ends at 24.036675ms
                                                      Gather#198:
                                                      Gather#198 step 1/2 (+0s): 9.953µs self time
                                                      Gather#198 step 2/2 (+9.953µs): return nil
                                                      Gather#198 ends at 24.046628ms
                                                  Gather#203 step 3/4 (+5.018µs): 4.981µs self time
                                                  Gather#203 step 4/4 (+9.999µs): return nil
                                                  Gather#203 ends at 23.94166ms
                                              Gather#204 step 3/8 (+2.492µs): 4.632µs self time
                                              Gather#204 step 4/8 (+7.124µs): scatter:
                                                Task#202: pool=0
                                                Task#202 step 1/2 (+0s): 98.862µs self time
                                                Task#202 step 2/2 (+98.862µs): return nil
                                                Task#202 ends at 388.029µs
                                                  Gather#202:
                                                  Gather#202 step 1/4 (+0s): 4.645µs self time
                                                  Gather#202 step 2/4 (+4.645µs): scatter:
                                                    Task#201: pool=0
                                                    Task#201 step 1/2 (+0s): 212.613µs self time
                                                    Task#201 step 2/2 (+212.613µs): return nil
                                                    Task#201 ends at 605.287µs
                                                      Combine#201:
                                                      Combine#201 step 1/4 (+0s): 4.931µs self time
                                                      Combine#201 step 2/4 (+4.931µs): scatter:
                                                        Task#200: pool=0
                                                        Task#200 step 1/2 (+0s): 99.467µs self time
                                                        Task#200 step 2/2 (+99.467µs): return nil
                                                        Task#200 ends at 709.685µs
                                                          Gather#200:
                                                          Gather#200 step 1/2 (+0s): 9.996µs self time
                                                          Gather#200 step 2/2 (+9.996µs): return nil
                                                          Gather#200 ends at 719.681µs
                                                      Combine#201 step 3/4 (+4.931µs): 5.056µs self time
                                                      Combine#201 step 4/4 (+9.987µs): return nil
                                                      Combine#201 ends at 615.274µs
                                                  Gather#202 step 3/4 (+4.645µs): 5.339µs self time
                                                  Gather#202 step 4/4 (+9.984µs): return nil
                                                  Gather#202 ends at 398.013µs
                                              Gather#204 step 5/8 (+7.124µs): 1.875µs self time
                                              Gather#204 step 6/8 (+8.999µs): scatter:
                                                Task#199: pool=0
                                                Task#199 step 1/2 (+0s): 99.998µs self time
                                                Task#199 step 2/2 (+99.998µs): return nil
                                                Task#199 ends at 391.04µs
                                                  Gather#199:
                                                  Gather#199 step 1/2 (+0s): 10.059µs self time
                                                  Gather#199 step 2/2 (+10.059µs): return nil
                                                  Gather#199 ends at 401.099µs
                                              Gather#204 step 7/8 (+8.999µs): 972ns self time
                                              Gather#204 step 8/8 (+9.971µs): return nil
                                              Gather#204 ends at 292.014µs
                                          Gather#205 step 3/4 (+4.998µs): 5µs self time
                                          Gather#205 step 4/4 (+9.998µs): return nil
                                          Gather#205 ends at 187.051µs
                                      Plan#15 step 2/2 (+0s): ends at 24.046628ms
                                    Gather#197 step 3/4 (+24.051173ms): 4.546µs self time
                                    Gather#197 step 4/4 (+24.055719ms): return nil
                                    Gather#197 ends at 24.289715ms
                                Gather#220 step 7/10 (+33.976µs): 831ns self time
                                Gather#220 step 8/10 (+34.807µs): scatter:
                                  Task#196: pool=5
                                  Task#196 step 1/2 (+0s): 99.992µs self time
                                  Task#196 step 2/2 (+99.992µs): return nil
                                  Task#196 ends at 234.691µs
                                    Gather#196:
                                    Gather#196 step 1/2 (+0s): 10.122µs self time
                                    Gather#196 step 2/2 (+10.122µs): return nil
                                    Gather#196 ends at 244.813µs
                                Gather#220 step 9/10 (+34.807µs): 101ns self time
                                Gather#220 step 10/10 (+34.908µs): return nil
                                Gather#220 ends at 134.8µs
                            Plan#14 step 2/2 (+0s): ends at 100.620527ms
                          Gather#195 step 3/6 (+100.62353ms): 3.805µs self time
                          Gather#195 step 4/6 (+100.627335ms): scatter:
                            Task#193: pool=0
                            Task#193 step 1/2 (+0s): 99.995µs self time
                            Task#193 step 2/2 (+99.995µs): return nil
                            Task#193 ends at 205.583653ms
                              Gather#193:
                              Gather#193 step 1/8 (+0s): 4.191µs self time
                              Gather#193 step 2/8 (+4.191µs): scatter:
                                Task#88: pool=0
                                Task#88 step 1/2 (+0s): 100µs self time
                                Task#88 step 2/2 (+100µs): return nil
                                Task#88 ends at 205.687844ms
                                  Gather#88:
                                  Gather#88 step 1/2 (+0s): 0s self time
                                  Gather#88 step 2/2 (+0s): return error
                                  Gather#88 ends at 205.687844ms
                              Gather#193 step 3/8 (+4.191µs): 4.199µs self time
                              Gather#193 step 4/8 (+8.39µs): scatter:
                                Task#192: pool=0
                                Task#192 step 1/2 (+0s): 103.393µs self time
                                Task#192 step 2/2 (+103.393µs): return nil
                                Task#192 ends at 205.695436ms
                                  Gather#192:
                                  Gather#192 step 1/6 (+0s): 3.499µs self time
                                  Gather#192 step 2/6 (+3.499µs): scatter:
                                    Task#140: pool=0
                                    Task#140 step 1/4 (+0s): 47.722µs self time
                                    Task#140 step 2/4 (+47.722µs): subjob:
                                      Plan#11: pathCount=3 taskCount=8 maxPathDuration=102.550858ms
                                      Plan#11 step 1/2 (+0s): scatter:
                                        Task#183: pool=0
                                        Task#183 step 1/2 (+0s): 10.639µs self time
                                        Task#183 step 2/2 (+10.639µs): return nil
                                        Task#183 ends at 10.639µs
                                          Gather#183:
                                          Gather#183 step 1/6 (+0s): 3.33µs self time
                                          Gather#183 step 2/6 (+3.33µs): scatter:
                                            Task#181: pool=1
                                            Task#181 step 1/2 (+0s): 99.986µs self time
                                            Task#181 step 2/2 (+99.986µs): return nil
                                            Task#181 ends at 113.955µs
                                              Gather#181:
                                              Gather#181 step 1/4 (+0s): 3.778µs self time
                                              Gather#181 step 2/4 (+3.778µs): scatter:
                                                Task#141: pool=1
                                                Task#141 step 1/4 (+0s): 947.602µs self time
                                                Task#141 step 2/4 (+947.602µs): subjob:
                                                  Plan#12: pathCount=13 taskCount=25 maxPathDuration=100.527473ms
                                                  Plan#12 step 1/4 (+0s): scatter:
                                                    Task#166: pool=1
                                                    Task#166 step 1/2 (+0s): 100.935µs self time
                                                    Task#166 step 2/2 (+100.935µs): return nil
                                                    Task#166 ends at 100.935µs
                                                      Gather#166:
                                                      Gather#166 step 1/4 (+0s): 4.903µs self time
                                                      Gather#166 step 2/4 (+4.903µs): scatter:
                                                        Task#146: pool=7
                                                        Task#146 step 1/2 (+0s): 100.009µs self time
                                                        Task#146 step 2/2 (+100.009µs): return nil
                                                        Task#146 ends at 205.847µs
                                                          Gather#146:
                                                          Gather#146 step 1/2 (+0s): 10.306µs self time
                                                          Gather#146 step 2/2 (+10.306µs): return nil
                                                          Gather#146 ends at 216.153µs
                                                      Gather#166 step 3/4 (+4.903µs): 4.929µs self time
                                                      Gather#166 step 4/4 (+9.832µs): return nil
                                                      Gather#166 ends at 110.767µs
                                                  Plan#12 step 2/4 (+0s): scatter:
                                                    Task#164: pool=3
                                                    Task#164 step 1/2 (+0s): 100.881µs self time
                                                    Task#164 step 2/2 (+100.881µs): return nil
                                                    Task#164 ends at 100.881µs
                                                      Gather#164:
                                                      Gather#164 step 1/8 (+0s): 1.557µs self time
                                                      Gather#164 step 2/8 (+1.557µs): scatter:
                                                        Task#163: pool=3
                                                        Task#163 step 1/2 (+0s): 99.997µs self time
                                                        Task#163 step 2/2 (+99.997µs): return nil
                                                        Task#163 ends at 202.435µs
                                                          Gather#163:
                                                          Gather#163 step 1/4 (+0s): 4.795µs self time
                                                          Gather#163 step 2/4 (+4.795µs): scatter:
                                                            Task#161: pool=4
                                                            Task#161 step 1/2 (+0s): 100.143µs self time
                                                            Task#161 step 2/2 (+100.143µs): return nil
                                                            Task#161 ends at 307.373µs
                                                              Combine#161:
                                                              Combine#161 step 1/4 (+0s): 2.33µs self time
                                                              Combine#161 step 2/4 (+2.33µs): scatter:
                                                                Task#159: pool=0
                                                                Task#159 step 1/2 (+0s): 101.598µs self time
                                                                Task#159 step 2/2 (+101.598µs): return nil
                                                                Task#159 ends at 411.301µs
                                                                  Gather#159:
                                                                  Gather#159 step 1/4 (+0s): 1.61µs self time
                                                                  Gather#159 step 2/4 (+1.61µs): scatter:
                                                                    Task#155: pool=0
                                                                    Task#155 step 1/2 (+0s): 100ms self time
                                                                    Task#155 step 2/2 (+100ms): return nil
                                                                    Task#155 ends at 100.412911ms
                                                                      Gather#155:
                                                                      Gather#155 step 1/4 (+0s): 9.984µs self time
                                                                      Gather#155 step 2/4 (+9.984µs): scatter:
                                                                        Task#151: pool=7
                                                                        Task#151 step 1/2 (+0s): 100.218µs self time
                                                                        Task#151 step 2/2 (+100.218µs): return nil
                                                                        Task#151 ends at 100.523113ms
                                                                          Gather#151:
                                                                          Gather#151 step 1/2 (+0s): 4.36µs self time
                                                                          Gather#151 step 2/2 (+4.36µs): return nil
                                                                          Gather#151 ends at 100.527473ms
                                                                      Gather#155 step 3/4 (+9.984µs): 0s self time
                                                                      Gather#155 step 4/4 (+9.984µs): return nil
                                                                      Gather#155 ends at 100.422895ms
                                                                  Gather#159 step 3/4 (+1.61µs): 773ns self time
                                                                  Gather#159 step 4/4 (+2.383µs): return nil
                                                                  Gather#159 ends at 413.684µs
                                                              Combine#161 step 3/4 (+2.33µs): 4.024µs self time
                                                              Combine#161 step 4/4 (+6.354µs): return nil
                                                              Combine#161 ends at 313.727µs
                                                          Gather#163 step 3/4 (+4.795µs): 4.783µs self time
                                                          Gather#163 step 4/4 (+9.578µs): return nil
                                                          Gather#163 ends at 212.013µs
                                                      Gather#164 step 3/8 (+1.557µs): 3.638µs self time
                                                      Gather#164 step 4/8 (+5.195µs): scatter:
                                                        Task#162: pool=1
                                                        Task#162 step 1/2 (+0s): 2.908449ms self time
                                                        Task#162 step 2/2 (+2.908449ms): return nil
                                                        Task#162 ends at 3.014525ms
                                                          Gather#162:
                                                          Gather#162 step 1/10 (+0s): 2.288µs self time
                                                          Gather#162 step 2/10 (+2.288µs): scatter:
                                                            Task#150: pool=5
                                                            Task#150 step 1/2 (+0s): 100.362µs self time
                                                            Task#150 step 2/2 (+100.362µs): return nil
                                                            Task#150 ends at 3.117175ms
                                                              Gather#150:
                                                              Gather#150 step 1/2 (+0s): 735ns self time
                                                              Gather#150 step 2/2 (+735ns): return nil
                                                              Gather#150 ends at 3.11791ms
                                                          Gather#162 step 3/10 (+2.288µs): 1.928µs self time
                                                          Gather#162 step 4/10 (+4.216µs): scatter:
                                                            Task#160: pool=5
                                                            Task#160 step 1/2 (+0s): 99.998µs self time
                                                            Task#160 step 2/2 (+99.998µs): return nil
                                                            Task#160 ends at 3.118739ms
                                                              Gather#160:
                                                              Gather#160 step 1/12 (+0s): 9.412µs self time
                                                              Gather#160 step 2/12 (+9.412µs): scatter:
                                                                Task#153: pool=1
                                                                Task#153 step 1/2 (+0s): 75.217µs self time
                                                                Task#153 step 2/2 (+75.217µs): return nil
                                                                Task#153 ends at 3.203368ms
                                                                  Gather#153:
                                                                  Gather#153 step 1/2 (+0s): 9.697µs self time
                                                                  Gather#153 step 2/2 (+9.697µs): return nil
                                                                  Gather#153 ends at 3.213065ms
                                                              Gather#160 step 3/12 (+9.412µs): 112ns self time
                                                              Gather#160 step 4/12 (+9.524µs): scatter:
                                                                Task#158: pool=7
                                                                Task#158 step 1/2 (+0s): 99.993µs self time
                                                                Task#158 step 2/2 (+99.993µs): return nil
                                                                Task#158 ends at 3.228256ms
                                                                  Gather#158:
                                                                  Gather#158 step 1/4 (+0s): 5.03µs self time
                                                                  Gather#158 step 2/4 (+5.03µs): scatter:
                                                                    Task#143: pool=1
                                                                    Task#143 step 1/2 (+0s): 100.005µs self time
                                                                    Task#143 step 2/2 (+100.005µs): return nil
                                                                    Task#143 ends at 3.333291ms
                                                                      Gather#143:
                                                                      Gather#143 step 1/2 (+0s): 7.926µs self time
                                                                      Gather#143 step 2/2 (+7.926µs): return nil
                                                                      Gather#143 ends at 3.341217ms
                                                                  Gather#158 step 3/4 (+5.03µs): 5.027µs self time
                                                                  Gather#158 step 4/4 (+10.057µs): return nil
                                                                  Gather#158 ends at 3.238313ms
                                                              Gather#160 step 5/12 (+9.524µs): 170ns self time
                                                              Gather#160 step 6/12 (+9.694µs): scatter:
                                                                Task#157: pool=4
                                                                Task#157 step 1/2 (+0s): 99.669µs self time
                                                                Task#157 step 2/2 (+99.669µs): return nil
                                                                Task#157 ends at 3.228102ms
                                                                  Gather#157:
                                                                  Gather#157 step 1/4 (+0s): 184.001µs self time
                                                                  Gather#157 step 2/4 (+184.001µs): scatter:
                                                                    Task#149: pool=1
                                                                    Task#149 step 1/2 (+0s): 109.505µs self time
                                                                    Task#149 step 2/2 (+109.505µs): return nil
                                                                    Task#149 ends at 3.521608ms
                                                                      Gather#149:
                                                                      Gather#149 step 1/2 (+0s): 9.998µs self time
                                                                      Gather#149 step 2/2 (+9.998µs): return error
                                                                      Gather#149 ends at 3.531606ms
                                                                  Gather#157 step 3/4 (+184.001µs): 184µs self time
                                                                  Gather#157 step 4/4 (+368.001µs): return nil
                                                                  Gather#157 ends at 3.596103ms
                                                              Gather#160 step 7/12 (+9.694µs): 99ns self time
                                                              Gather#160 step 8/12 (+9.793µs): scatter:
                                                                Task#154: pool=6
                                                                Task#154 step 1/2 (+0s): 100.005µs self time
                                                                Task#154 step 2/2 (+100.005µs): return nil
                                                                Task#154 ends at 3.228537ms
                                                                  Gather#154:
                                                                  Gather#154 step 1/2 (+0s): 9.735µs self time
                                                                  Gather#154 step 2/2 (+9.735µs): return nil
                                                                  Gather#154 ends at 3.238272ms
                                                              Gather#160 step 9/12 (+9.793µs): 109ns self time
                                                              Gather#160 step 10/12 (+9.902µs): scatter:
                                                                Task#156: pool=6
                                                                Task#156 step 1/2 (+0s): 99.994µs self time
                                                                Task#156 step 2/2 (+99.994µs): return nil
                                                                Task#156 ends at 3.228635ms
                                                                  Combine#156:
                                                                  Combine#156 step 1/4 (+0s): 4.971µs self time
                                                                  Combine#156 step 2/4 (+4.971µs): scatter:
                                                                    Task#152: pool=1
                                                                    Task#152 step 1/2 (+0s): 99.998µs self time
                                                                    Task#152 step 2/2 (+99.998µs): return nil
                                                                    Task#152 ends at 3.333604ms
                                                                      Gather#152:
                                                                      Gather#152 step 1/2 (+0s): 9.995µs self time
                                                                      Gather#152 step 2/2 (+9.995µs): return nil
                                                                      Gather#152 ends at 3.343599ms
                                                                  Combine#156 step 3/4 (+4.971µs): 4.971µs self time
                                                                  Combine#156 step 4/4 (+9.942µs): return nil
                                                                  Combine#156 ends at 3.238577ms
                                                              Gather#160 step 11/12 (+9.902µs): 97ns self time
                                                              Gather#160 step 12/12 (+9.999µs): return nil
                                                              Gather#160 ends at 3.128738ms
                                                          Gather#162 step 5/10 (+4.216µs): 1.803µs self time
                                                          Gather#162 step 6/10 (+6.019µs): scatter:
                                                            Task#142: pool=0
                                                            Task#142 step 1/2 (+0s): 175.854µs self time
                                                            Task#142 step 2/2 (+175.854µs): return nil
                                                            Task#142 ends at 3.196398ms
                                                              Gather#142:
                                                              Gather#142 step 1/2 (+0s): 10.001µs self time
                                                              Gather#142 step 2/2 (+10.001µs): return nil
                                                              Gather#142 ends at 3.206399ms
                                                          Gather#162 step 7/10 (+6.019µs): 1.985µs self time
                                                          Gather#162 step 8/10 (+8.004µs): scatter:
                                                            Task#144: pool=7
                                                            Task#144 step 1/2 (+0s): 99.999µs self time
                                                            Task#144 step 2/2 (+99.999µs): return nil
                                                            Task#144 ends at 3.122528ms
                                                              Gather#144:
                                                              Gather#144 step 1/2 (+0s): 9.969µs self time
                                                              Gather#144 step 2/2 (+9.969µs): return nil
                                                              Gather#144 ends at 3.132497ms
                                                          Gather#162 step 9/10 (+8.004µs): 1.997µs self time
                                                          Gather#162 step 10/10 (+10.001µs): return nil
                                                          Gather#162 ends at 3.024526ms
                                                      Gather#164 step 5/8 (+5.195µs): 4.349µs self time
                                                      Gather#164 step 6/8 (+9.544µs): scatter:
                                                        Task#147: pool=5
                                                        Task#147 step 1/2 (+0s): 100.001µs self time
                                                        Task#147 step 2/2 (+100.001µs): return error
                                                        Task#147 ends at 210.426µs
                                                          Gather#147:
                                                          Gather#147 step 1/2 (+0s): 9.999µs self time
                                                          Gather#147 step 2/2 (+9.999µs): return nil
                                                          Gather#147 ends at 220.425µs
                                                      Gather#164 step 7/8 (+9.544µs): 2.584µs self time
                                                      Gather#164 step 8/8 (+12.128µs): return nil
                                                      Gather#164 ends at 113.009µs
                                                  Plan#12 step 3/4 (+0s): scatter:
                                                    Task#165: pool=7
                                                    Task#165 step 1/2 (+0s): 99.998µs self time
                                                    Task#165 step 2/2 (+99.998µs): return nil
                                                    Task#165 ends at 99.998µs
                                                      Gather#165:
                                                      Gather#165 step 1/6 (+0s): 13.502µs self time
                                                      Gather#165 step 2/6 (+13.502µs): scatter:
                                                        Task#148: pool=5
                                                        Task#148 step 1/2 (+0s): 99.968µs self time
                                                        Task#148 step 2/2 (+99.968µs): return nil
                                                        Task#148 ends at 213.468µs
                                                          Combine#148:
                                                          Combine#148 step 1/2 (+0s): 9.998µs self time
                                                          Combine#148 step 2/2 (+9.998µs): return nil
                                                          Combine#148 ends at 223.466µs
                                                      Gather#165 step 3/6 (+13.502µs): 27.21µs self time
                                                      Gather#165 step 4/6 (+40.712µs): scatter:
                                                        Task#145: pool=3
                                                        Task#145 step 1/2 (+0s): 100.002µs self time
                                                        Task#145 step 2/2 (+100.002µs): return nil
                                                        Task#145 ends at 240.712µs
                                                          Gather#145:
                                                          Gather#145 step 1/2 (+0s): 10µs self time
                                                          Gather#145 step 2/2 (+10µs): return nil
                                                          Gather#145 ends at 250.712µs
                                                      Gather#165 step 5/6 (+40.712µs): 0s self time
                                                      Gather#165 step 6/6 (+40.712µs): return nil
                                                      Gather#165 ends at 140.71µs
                                                  Plan#12 step 4/4 (+0s): ends at 100.527473ms
                                                Task#141 step 3/4 (+101.475075ms): 948.048µs self time
                                                Task#141 step 4/4 (+102.423123ms): return nil
                                                Task#141 ends at 102.540856ms
                                                  Gather#141:
                                                  Gather#141 step 1/2 (+0s): 10.002µs self time
                                                  Gather#141 step 2/2 (+10.002µs): return nil
                                                  Gather#141 ends at 102.550858ms
                                              Gather#181 step 3/4 (+3.778µs): 3.787µs self time
                                              Gather#181 step 4/4 (+7.565µs): return nil
                                              Gather#181 ends at 121.52µs
                                          Gather#183 step 3/6 (+3.33µs): 3.793µs self time
                                          Gather#183 step 4/6 (+7.123µs): scatter:
                                            Task#182: pool=0
                                            Task#182 step 1/2 (+0s): 100.01µs self time
                                            Task#182 step 2/2 (+100.01µs): return nil
                                            Task#182 ends at 117.772µs
                                              Gather#182:
                                              Gather#182 step 1/4 (+0s): 4.997µs self time
                                              Gather#182 step 2/4 (+4.997µs): scatter:
                                                Task#180: pool=1
                                                Task#180 step 1/2 (+0s): 98.076µs self time
                                                Task#180 step 2/2 (+98.076µs): return nil
                                                Task#180 ends at 220.845µs
                                                  Gather#180:
                                                  Gather#180 step 1/6 (+0s): 3.396µs self time
                                                  Gather#180 step 2/6 (+3.396µs): scatter:
                                                    Task#169: pool=0
                                                    Task#169 step 1/4 (+0s): 51.625µs self time
                                                    Task#169 step 2/4 (+51.625µs): subjob:
                                                      Plan#13: pathCount=4 taskCount=10 maxPathDuration=3.300484ms
                                                      Plan#13 step 1/2 (+0s): scatter:
                                                        Task#179: pool=0
                                                        Task#179 step 1/2 (+0s): 100.007µs self time
                                                        Task#179 step 2/2 (+100.007µs): return nil
                                                        Task#179 ends at 100.007µs
                                                          Gather#179:
                                                          Gather#179 step 1/6 (+0s): 3.33µs self time
                                                          Gather#179 step 2/6 (+3.33µs): scatter:
                                                            Task#178: pool=0
                                                            Task#178 step 1/2 (+0s): 99.955µs self time
                                                            Task#178 step 2/2 (+99.955µs): return nil
                                                            Task#178 ends at 203.292µs
                                                              Combine#178:
                                                              Combine#178 step 1/6 (+0s): 3.337µs self time
                                                              Combine#178 step 2/6 (+3.337µs): scatter:
                                                                Task#176: pool=0
                                                                Task#176 step 1/2 (+0s): 99.996µs self time
                                                                Task#176 step 2/2 (+99.996µs): return nil
                                                                Task#176 ends at 306.625µs
                                                                  Gather#176:
                                                                  Gather#176 step 1/4 (+0s): 1.496928ms self time
                                                                  Gather#176 step 2/4 (+1.496928ms): scatter:
                                                                    Task#175: pool=0
                                                                    Task#175 step 1/2 (+0s): 99.81µs self time
                                                                    Task#175 step 2/2 (+99.81µs): return nil
                                                                    Task#175 ends at 1.903363ms
                                                                      Gather#175:
                                                                      Gather#175 step 1/4 (+0s): 7.926µs self time
                                                                      Gather#175 step 2/4 (+7.926µs): scatter:
                                                                        Task#174: pool=0
                                                                        Task#174 step 1/2 (+0s): 100.494µs self time
                                                                        Task#174 step 2/2 (+100.494µs): return nil
                                                                        Task#174 ends at 2.011783ms
                                                                          Gather#174:
                                                                          Gather#174 step 1/4 (+0s): 7.034µs self time
                                                                          Gather#174 step 2/4 (+7.034µs): scatter:
                                                                            Task#173: pool=0
                                                                            Task#173 step 1/2 (+0s): 99.89µs self time
                                                                            Task#173 step 2/2 (+99.89µs): return nil
                                                                            Task#173 ends at 2.118707ms
                                                                              Gather#173:
                                                                              Gather#173 step 1/2 (+0s): 10.168µs self time
                                                                              Gather#173 step 2/2 (+10.168µs): return nil
                                                                              Gather#173 ends at 2.128875ms
                                                                          Gather#174 step 3/4 (+7.034µs): 2.932µs self time
                                                                          Gather#174 step 4/4 (+9.966µs): return nil
                                                                          Gather#174 ends at 2.021749ms
                                                                      Gather#175 step 3/4 (+7.926µs): 1.558µs self time
                                                                      Gather#175 step 4/4 (+9.484µs): return nil
                                                                      Gather#175 ends at 1.912847ms
                                                                  Gather#176 step 3/4 (+1.496928ms): 1.496931ms self time
                                                                  Gather#176 step 4/4 (+2.993859ms): return nil
                                                                  Gather#176 ends at 3.300484ms
                                                              Combine#178 step 3/6 (+3.337µs): 3.107µs self time
                                                              Combine#178 step 4/6 (+6.444µs): scatter:
                                                                Task#177: pool=0
                                                                Task#177 step 1/2 (+0s): 103.168µs self time
                                                                Task#177 step 2/2 (+103.168µs): return nil
                                                                Task#177 ends at 312.904µs
                                                                  Gather#177:
                                                                  Gather#177 step 1/6 (+0s): 2.329µs self time
                                                                  Gather#177 step 2/6 (+2.329µs): scatter:
                                                                    Task#172: pool=0
                                                                    Task#172 step 1/2 (+0s): 100.006µs self time
                                                                    Task#172 step 2/2 (+100.006µs): return nil
                                                                    Task#172 ends at 415.239µs
                                                                      Gather#172:
                                                                      Gather#172 step 1/2 (+0s): 9.696µs self time
                                                                      Gather#172 step 2/2 (+9.696µs): return nil
                                                                      Gather#172 ends at 424.935µs
                                                                  Gather#177 step 3/6 (+2.329µs): 2.438µs self time
                                                                  Gather#177 step 4/6 (+4.767µs): scatter:
                                                                    Task#171: pool=0
                                                                    Task#171 step 1/2 (+0s): 99.939µs self time
                                                                    Task#171 step 2/2 (+99.939µs): return nil
                                                                    Task#171 ends at 417.61µs
                                                                      Gather#171:
                                                                      Gather#171 step 1/2 (+0s): 9.884µs self time
                                                                      Gather#171 step 2/2 (+9.884µs): return nil
                                                                      Gather#171 ends at 427.494µs
                                                                  Gather#177 step 5/6 (+4.767µs): 2.026µs self time
                                                                  Gather#177 step 6/6 (+6.793µs): return nil
                                                                  Gather#177 ends at 319.697µs
                                                              Combine#178 step 5/6 (+6.444µs): 3.538µs self time
                                                              Combine#178 step 6/6 (+9.982µs): return nil
                                                              Combine#178 ends at 213.274µs
                                                          Gather#179 step 3/6 (+3.33µs): 6.265µs self time
                                                          Gather#179 step 4/6 (+9.595µs): scatter:
                                                            Task#170: pool=0
                                                            Task#170 step 1/2 (+0s): 100.039µs self time
                                                            Task#170 step 2/2 (+100.039µs): return nil
                                                            Task#170 ends at 209.641µs
                                                              Gather#170:
                                                              Gather#170 step 1/2 (+0s): 10.004µs self time
                                                              Gather#170 step 2/2 (+10.004µs): return nil
                                                              Gather#170 ends at 219.645µs
                                                          Gather#179 step 5/6 (+9.595µs): 403ns self time
                                                          Gather#179 step 6/6 (+9.998µs): return nil
                                                          Gather#179 ends at 110.005µs
                                                      Plan#13 step 2/2 (+0s): ends at 3.300484ms
                                                    Task#169 step 3/4 (+3.352109ms): 51.658µs self time
                                                    Task#169 step 4/4 (+3.403767ms): return nil
                                                    Task#169 ends at 3.628008ms
                                                      Gather#169:
                                                      Gather#169 step 1/4 (+0s): 4.993µs self time
                                                      Gather#169 step 2/4 (+4.993µs): scatter:
                                                        Task#167: pool=1
                                                        Task#167 step 1/2 (+0s): 99.497µs self time
                                                        Task#167 step 2/2 (+99.497µs): return nil
                                                        Task#167 ends at 3.732498ms
                                                          Combine#167:
                                                          Combine#167 step 1/2 (+0s): 1.586µs self time
                                                          Combine#167 step 2/2 (+1.586µs): return nil
                                                          Combine#167 ends at 3.734084ms
                                                      Gather#169 step 3/4 (+4.993µs): 5.005µs self time
                                                      Gather#169 step 4/4 (+9.998µs): return nil
                                                      Gather#169 ends at 3.638006ms
                                                  Gather#180 step 3/6 (+3.396µs): 4.769µs self time
                                                  Gather#180 step 4/6 (+8.165µs): scatter:
                                                    Task#168: pool=1
                                                    Task#168 step 1/2 (+0s): 100.001µs self time
                                                    Task#168 step 2/2 (+100.001µs): return nil
                                                    Task#168 ends at 329.011µs
                                                      Gather#168:
                                                      Gather#168 step 1/2 (+0s): 9.996µs self time
                                                      Gather#168 step 2/2 (+9.996µs): return nil
                                                      Gather#168 ends at 339.007µs
                                                  Gather#180 step 5/6 (+8.165µs): 2.028µs self time
                                                  Gather#180 step 6/6 (+10.193µs): return nil
                                                  Gather#180 ends at 231.038µs
                                              Gather#182 step 3/4 (+4.997µs): 5.002µs self time
                                              Gather#182 step 4/4 (+9.999µs): return nil
                                              Gather#182 ends at 127.771µs
                                          Gather#183 step 5/6 (+7.123µs): 2.867µs self time
                                          Gather#183 step 6/6 (+9.99µs): return nil
                                          Gather#183 ends at 20.629µs
                                      Plan#11 step 2/2 (+0s): ends at 102.550858ms
                                    Task#140 step 3/4 (+102.59858ms): 52.223µs self time
                                    Task#140 step 4/4 (+102.650803ms): return nil
                                    Task#140 ends at 308.349738ms
                                      Gather#140:
                                      Gather#140 step 1/2 (+0s): 10.397µs self time
                                      Gather#140 step 2/2 (+10.397µs): return nil
                                      Gather#140 ends at 308.360135ms
                                  Gather#192 step 3/6 (+3.499µs): 3.5µs self time
                                  Gather#192 step 4/6 (+6.999µs): scatter:
                                    Task#91: pool=0
                                    Task#91 step 1/2 (+0s): 90.878µs self time
                                    Task#91 step 2/2 (+90.878µs): return nil
                                    Task#91 ends at 205.793313ms
                                      Gather#91:
                                      Gather#91 step 1/2 (+0s): 9.847µs self time
                                      Gather#91 step 2/2 (+9.847µs): return nil
                                      Gather#91 ends at 205.80316ms
                                  Gather#192 step 5/6 (+6.999µs): 3.502µs self time
                                  Gather#192 step 6/6 (+10.501µs): return nil
                                  Gather#192 ends at 205.705937ms
                              Gather#193 step 5/8 (+8.39µs): 4.201µs self time
                              Gather#193 step 6/8 (+12.591µs): scatter:
                                Task#94: pool=0
                                Task#94 step 1/4 (+0s): 50.723µs self time
                                Task#94 step 2/4 (+50.723µs): subjob:
                                  Plan#8: pathCount=3 taskCount=9 maxPathDuration=58.43898ms
                                  Plan#8 step 1/3 (+0s): scatter:
                                    Task#109: pool=0
                                    Task#109 step 1/2 (+0s): 99.994µs self time
                                    Task#109 step 2/2 (+99.994µs): return nil
                                    Task#109 ends at 99.994µs
                                      Gather#109:
                                      Gather#109 step 1/6 (+0s): 3.338µs self time
                                      Gather#109 step 2/6 (+3.338µs): subjob:
                                        Plan#10: pathCount=20 taskCount=29 maxPathDuration=32.572437ms
                                        Plan#10 step 1/6 (+0s): scatter:
                                          Task#135: pool=0
                                          Task#135 step 1/2 (+0s): 96.762µs self time
                                          Task#135 step 2/2 (+96.762µs): return nil
                                          Task#135 ends at 96.762µs
                                            Gather#135:
                                            Gather#135 step 1/6 (+0s): 1.67µs self time
                                            Gather#135 step 2/6 (+1.67µs): scatter:
                                              Task#128: pool=2
                                              Task#128 step 1/2 (+0s): 100.822µs self time
                                              Task#128 step 2/2 (+100.822µs): return error
                                              Task#128 ends at 199.254µs
                                                Combine#128:
                                                Combine#128 step 1/2 (+0s): 6.865596ms self time
                                                Combine#128 step 2/2 (+6.865596ms): return nil
                                                Combine#128 ends at 7.06485ms
                                            Gather#135 step 3/6 (+1.67µs): 9.179µs self time
                                            Gather#135 step 4/6 (+10.849µs): scatter:
                                              Task#133: pool=0
                                              Task#133 step 1/2 (+0s): 100µs self time
                                              Task#133 step 2/2 (+100µs): return nil
                                              Task#133 ends at 207.611µs
                                                Gather#133:
                                                Gather#133 step 1/14 (+0s): 535ns self time
                                                Gather#133 step 2/14 (+535ns): scatter:
                                                  Task#132: pool=1
                                                  Task#132 step 1/2 (+0s): 99.974µs self time
                                                  Task#132 step 2/2 (+99.974µs): return nil
                                                  Task#132 ends at 308.12µs
                                                    Gather#132:
                                                    Gather#132 step 1/10 (+0s): 9.264µs self time
                                                    Gather#132 step 2/10 (+9.264µs): scatter:
                                                      Task#126: pool=1
                                                      Task#126 step 1/2 (+0s): 100.237µs self time
                                                      Task#126 step 2/2 (+100.237µs): return nil
                                                      Task#126 ends at 417.621µs
                                                        Gather#126:
                                                        Gather#126 step 1/2 (+0s): 10ms self time
                                                        Gather#126 step 2/2 (+10ms): return nil
                                                        Gather#126 ends at 10.417621ms
                                                    Gather#132 step 3/10 (+9.264µs): 4ns self time
                                                    Gather#132 step 4/10 (+9.268µs): scatter:
                                                      Task#121: pool=1
                                                      Task#121 step 1/2 (+0s): 100.342µs self time
                                                      Task#121 step 2/2 (+100.342µs): return nil
                                                      Task#121 ends at 417.73µs
                                                        Gather#121:
                                                        Gather#121 step 1/2 (+0s): 10.195µs self time
                                                        Gather#121 step 2/2 (+10.195µs): return nil
                                                        Gather#121 ends at 427.925µs
                                                    Gather#132 step 5/10 (+9.268µs): 78ns self time
                                                    Gather#132 step 6/10 (+9.346µs): scatter:
                                                      Task#115: pool=3
                                                      Task#115 step 1/2 (+0s): 100.784µs self time
                                                      Task#115 step 2/2 (+100.784µs): return nil
                                                      Task#115 ends at 418.25µs
                                                        Gather#115:
                                                        Gather#115 step 1/2 (+0s): 9.49µs self time
                                                        Gather#115 step 2/2 (+9.49µs): return nil
                                                        Gather#115 ends at 427.74µs
                                                    Gather#132 step 7/10 (+9.346µs): 324ns self time
                                                    Gather#132 step 8/10 (+9.67µs): scatter:
                                                      Task#131: pool=1
                                                      Task#131 step 1/2 (+0s): 42.219µs self time
                                                      Task#131 step 2/2 (+42.219µs): return nil
                                                      Task#131 ends at 360.009µs
                                                        Gather#131:
                                                        Gather#131 step 1/12 (+0s): 426ns self time
                                                        Gather#131 step 2/12 (+426ns): scatter:
                                                          Task#110: pool=0
                                                          Task#110 step 1/2 (+0s): 99.958µs self time
                                                          Task#110 step 2/2 (+99.958µs): return nil
                                                          Task#110 ends at 460.393µs
                                                            Gather#110:
                                                            Gather#110 step 1/2 (+0s): 10µs self time
                                                            Gather#110 step 2/2 (+10µs): return nil
                                                            Gather#110 ends at 470.393µs
                                                        Gather#131 step 3/12 (+426ns): 1.288µs self time
                                                        Gather#131 step 4/12 (+1.714µs): scatter:
                                                          Task#116: pool=3
                                                          Task#116 step 1/2 (+0s): 49.073µs self time
                                                          Task#116 step 2/2 (+49.073µs): return nil
                                                          Task#116 ends at 410.796µs
                                                            Gather#116:
                                                            Gather#116 step 1/2 (+0s): 17.636µs self time
                                                            Gather#116 step 2/2 (+17.636µs): return nil
                                                            Gather#116 ends at 428.432µs
                                                        Gather#131 step 5/12 (+1.714µs): 204ns self time
                                                        Gather#131 step 6/12 (+1.918µs): scatter:
                                                          Task#125: pool=0
                                                          Task#125 step 1/2 (+0s): 100.001µs self time
                                                          Task#125 step 2/2 (+100.001µs): return nil
                                                          Task#125 ends at 461.928µs
                                                            Gather#125:
                                                            Gather#125 step 1/2 (+0s): 907ns self time
                                                            Gather#125 step 2/2 (+907ns): return nil
                                                            Gather#125 ends at 462.835µs
                                                        Gather#131 step 7/12 (+1.918µs): 450ns self time
                                                        Gather#131 step 8/12 (+2.368µs): scatter:
                                                          Task#130: pool=2
                                                          Task#130 step 1/2 (+0s): 100.044µs self time
                                                          Task#130 step 2/2 (+100.044µs): return nil
                                                          Task#130 ends at 462.421µs
                                                            Combine#130:
                                                            Combine#130 step 1/6 (+0s): 2.484µs self time
                                                            Combine#130 step 2/6 (+2.484µs): scatter:
                                                              Task#129: pool=2
                                                              Task#129 step 1/2 (+0s): 100.013µs self time
                                                              Task#129 step 2/2 (+100.013µs): return nil
                                                              Task#129 ends at 564.918µs
                                                                Gather#129:
                                                                Gather#129 step 1/2 (+0s): 9.927µs self time
                                                                Gather#129 step 2/2 (+9.927µs): return nil
                                                                Gather#129 ends at 574.845µs
                                                            Combine#130 step 3/6 (+2.484µs): 2.209µs self time
                                                            Combine#130 step 4/6 (+4.693µs): scatter:
                                                              Task#122: pool=1
                                                              Task#122 step 1/2 (+0s): 99.999µs self time
                                                              Task#122 step 2/2 (+99.999µs): return nil
                                                              Task#122 ends at 567.113µs
                                                                Gather#122:
                                                                Gather#122 step 1/2 (+0s): 10.107µs self time
                                                                Gather#122 step 2/2 (+10.107µs): return nil
                                                                Gather#122 ends at 577.22µs
                                                            Combine#130 step 5/6 (+4.693µs): 2.626µs self time
                                                            Combine#130 step 6/6 (+7.319µs): return nil
                                                            Combine#130 ends at 469.74µs
                                                        Gather#131 step 9/12 (+2.368µs): 37ns self time
                                                        Gather#131 step 10/12 (+2.405µs): scatter:
                                                          Task#124: pool=0
                                                          Task#124 step 1/2 (+0s): 100.059µs self time
                                                          Task#124 step 2/2 (+100.059µs): return nil
                                                          Task#124 ends at 462.473µs
                                                            Gather#124:
                                                            Gather#124 step 1/2 (+0s): 8.683µs self time
                                                            Gather#124 step 2/2 (+8.683µs): return nil
                                                            Gather#124 ends at 471.156µs
                                                        Gather#131 step 11/12 (+2.405µs): 131ns self time
                                                        Gather#131 step 12/12 (+2.536µs): return nil
                                                        Gather#131 ends at 362.545µs
                                                    Gather#132 step 9/10 (+9.67µs): 328ns self time
                                                    Gather#132 step 10/10 (+9.998µs): return nil
                                                    Gather#132 ends at 318.118µs
                                                Gather#133 step 3/14 (+535ns): 1.73µs self time
                                                Gather#133 step 4/14 (+2.265µs): scatter:
                                                  Task#120: pool=1
                                                  Task#120 step 1/2 (+0s): 99.997µs self time
                                                  Task#120 step 2/2 (+99.997µs): return nil
                                                  Task#120 ends at 309.873µs
                                                    Gather#120:
                                                    Gather#120 step 1/2 (+0s): 9.993µs self time
                                                    Gather#120 step 2/2 (+9.993µs): return error
                                                    Gather#120 ends at 319.866µs
                                                Gather#133 step 5/14 (+2.265µs): 320ns self time
                                                Gather#133 step 6/14 (+2.585µs): scatter:
                                                  Task#123: pool=2
                                                  Task#123 step 1/2 (+0s): 99.882µs self time
                                                  Task#123 step 2/2 (+99.882µs): return nil
                                                  Task#123 ends at 310.078µs
                                                    Gather#123:
                                                    Gather#123 step 1/2 (+0s): 10µs self time
                                                    Gather#123 step 2/2 (+10µs): return nil
                                                    Gather#123 ends at 320.078µs
                                                Gather#133 step 7/14 (+2.585µs): 2.073µs self time
                                                Gather#133 step 8/14 (+4.658µs): scatter:
                                                  Task#127: pool=0
                                                  Task#127 step 1/2 (+0s): 99.996µs self time
                                                  Task#127 step 2/2 (+99.996µs): return nil
                                                  Task#127 ends at 312.265µs
                                                    Gather#127:
                                                    Gather#127 step 1/2 (+0s): 3.036µs self time
                                                    Gather#127 step 2/2 (+3.036µs): return nil
                                                    Gather#127 ends at 315.301µs
                                                Gather#133 step 9/14 (+4.658µs): 2.089µs self time
                                                Gather#133 step 10/14 (+6.747µs): scatter:
                                                  Task#117: pool=3
                                                  Task#117 step 1/2 (+0s): 99.999µs self time
                                                  Task#117 step 2/2 (+99.999µs): return nil
                                                  Task#117 ends at 314.357µs
                                                    Gather#117:
                                                    Gather#117 step 1/2 (+0s): 9.995µs self time
                                                    Gather#117 step 2/2 (+9.995µs): return nil
                                                    Gather#117 ends at 324.352µs
                                                Gather#133 step 11/14 (+6.747µs): 2.133µs self time
                                                Gather#133 step 12/14 (+8.88µs): scatter:
                                                  Task#111: pool=3
                                                  Task#111 step 1/2 (+0s): 100.764µs self time
                                                  Task#111 step 2/2 (+100.764µs): return nil
                                                  Task#111 ends at 317.255µs
                                                    Gather#111:
                                                    Gather#111 step 1/2 (+0s): 9.984µs self time
                                                    Gather#111 step 2/2 (+9.984µs): return nil
                                                    Gather#111 ends at 327.239µs
                                                Gather#133 step 13/14 (+8.88µs): 2.038µs self time
                                                Gather#133 step 14/14 (+10.918µs): return nil
                                                Gather#133 ends at 218.529µs
                                            Gather#135 step 5/6 (+10.849µs): 0s self time
                                            Gather#135 step 6/6 (+10.849µs): return nil
                                            Gather#135 ends at 107.611µs
                                        Plan#10 step 2/6 (+0s): scatter:
                                          Task#138: pool=1
                                          Task#138 step 1/2 (+0s): 99.984µs self time
                                          Task#138 step 2/2 (+99.984µs): return nil
                                          Task#138 ends at 99.984µs
                                            Gather#138:
                                            Gather#138 step 1/4 (+0s): 14ns self time
                                            Gather#138 step 2/4 (+14ns): scatter:
                                              Task#114: pool=1
                                              Task#114 step 1/2 (+0s): 99.996µs self time
                                              Task#114 step 2/2 (+99.996µs): return nil
                                              Task#114 ends at 199.994µs
                                                Gather#114:
                                                Gather#114 step 1/2 (+0s): 9.565µs self time
                                                Gather#114 step 2/2 (+9.565µs): return nil
                                                Gather#114 ends at 209.559µs
                                            Gather#138 step 3/4 (+14ns): 27ns self time
                                            Gather#138 step 4/4 (+41ns): return nil
                                            Gather#138 ends at 100.025µs
                                        Plan#10 step 3/6 (+0s): scatter:
                                          Task#137: pool=2
                                          Task#137 step 1/2 (+0s): 40.892µs self time
                                          Task#137 step 2/2 (+40.892µs): return nil
                                          Task#137 ends at 40.892µs
                                            Gather#137:
                                            Gather#137 step 1/6 (+0s): 5.053µs self time
                                            Gather#137 step 2/6 (+5.053µs): scatter:
                                              Task#118: pool=0
                                              Task#118 step 1/2 (+0s): 100.002µs self time
                                              Task#118 step 2/2 (+100.002µs): return nil
                                              Task#118 ends at 145.947µs
                                                Gather#118:
                                                Gather#118 step 1/2 (+0s): 4.152485ms self time
                                                Gather#118 step 2/2 (+4.152485ms): return nil
                                                Gather#118 ends at 4.298432ms
                                            Gather#137 step 3/6 (+5.053µs): 5.059µs self time
                                            Gather#137 step 4/6 (+10.112µs): scatter:
                                              Task#113: pool=1
                                              Task#113 step 1/2 (+0s): 100.219µs self time
                                              Task#113 step 2/2 (+100.219µs): return nil
                                              Task#113 ends at 151.223µs
                                                Gather#113:
                                                Gather#113 step 1/2 (+0s): 9.997µs self time
                                                Gather#113 step 2/2 (+9.997µs): return nil
                                                Gather#113 ends at 161.22µs
                                            Gather#137 step 5/6 (+10.112µs): 5.046µs self time
                                            Gather#137 step 6/6 (+15.158µs): return nil
                                            Gather#137 ends at 56.05µs
                                        Plan#10 step 4/6 (+0s): scatter:
                                          Task#136: pool=3
                                          Task#136 step 1/2 (+0s): 100.003µs self time
                                          Task#136 step 2/2 (+100.003µs): return nil
                                          Task#136 ends at 100.003µs
                                            Gather#136:
                                            Gather#136 step 1/4 (+0s): 3.238µs self time
                                            Gather#136 step 2/4 (+3.238µs): scatter:
                                              Task#119: pool=3
                                              Task#119 step 1/2 (+0s): 100.001µs self time
                                              Task#119 step 2/2 (+100.001µs): return nil
                                              Task#119 ends at 203.242µs
                                                Gather#119:
                                                Gather#119 step 1/2 (+0s): 123.441µs self time
                                                Gather#119 step 2/2 (+123.441µs): return nil
                                                Gather#119 ends at 326.683µs
                                            Gather#136 step 3/4 (+3.238µs): 3.243µs self time
                                            Gather#136 step 4/4 (+6.481µs): return nil
                                            Gather#136 ends at 106.484µs
                                        Plan#10 step 5/6 (+0s): scatter:
                                          Task#134: pool=0
                                          Task#134 step 1/2 (+0s): 99.941µs self time
                                          Task#134 step 2/2 (+99.941µs): return nil
                                          Task#134 ends at 99.941µs
                                            Gather#134:
                                            Gather#134 step 1/4 (+0s): 974.297µs self time
                                            Gather#134 step 2/4 (+974.297µs): scatter:
                                              Task#112: pool=2
                                              Task#112 step 1/2 (+0s): 31.487754ms self time
                                              Task#112 step 2/2 (+31.487754ms): return nil
                                              Task#112 ends at 32.561992ms
                                                Gather#112:
                                                Gather#112 step 1/2 (+0s): 10.445µs self time
                                                Gather#112 step 2/2 (+10.445µs): return nil
                                                Gather#112 ends at 32.572437ms
                                            Gather#134 step 3/4 (+974.297µs): 974.292µs self time
                                            Gather#134 step 4/4 (+1.948589ms): return nil
                                            Gather#134 ends at 2.04853ms
                                        Plan#10 step 6/6 (+0s): ends at 32.572437ms
                                      Gather#109 step 3/6 (+32.575775ms): 2.916µs self time
                                      Gather#109 step 4/6 (+32.578691ms): scatter:
                                        Task#100: pool=0
                                        Task#100 step 1/2 (+0s): 123.189µs self time
                                        Task#100 step 2/2 (+123.189µs): return nil
                                        Task#100 ends at 32.801874ms
                                          Gather#100:
                                          Gather#100 step 1/4 (+0s): 5.006µs self time
                                          Gather#100 step 2/4 (+5.006µs): scatter:
                                            Task#95: pool=0
                                            Task#95 step 1/2 (+0s): 100.117µs self time
                                            Task#95 step 2/2 (+100.117µs): return error
                                            Task#95 ends at 32.906997ms
                                              Gather#95:
                                              Gather#95 step 1/2 (+0s): 3.753973ms self time
                                              Gather#95 step 2/2 (+3.753973ms): return nil
                                              Gather#95 ends at 36.66097ms
                                          Gather#100 step 3/4 (+5.006µs): 5µs self time
                                          Gather#100 step 4/4 (+10.006µs): return nil
                                          Gather#100 ends at 32.81188ms
                                      Gather#109 step 5/6 (+32.578691ms): 3.747µs self time
                                      Gather#109 step 6/6 (+32.582438ms): return error
                                      Gather#109 ends at 32.682432ms
                                  Plan#8 step 2/3 (+0s): scatter:
                                    Task#108: pool=0
                                    Task#108 step 1/2 (+0s): 99.946µs self time
                                    Task#108 step 2/2 (+99.946µs): return nil
                                    Task#108 ends at 99.946µs
                                      Gather#108:
                                      Gather#108 step 1/6 (+0s): 3.331µs self time
                                      Gather#108 step 2/6 (+3.331µs): scatter:
                                        Task#97: pool=0
                                        Task#97 step 1/2 (+0s): 58.325711ms self time
                                        Task#97 step 2/2 (+58.325711ms): return nil
                                        Task#97 ends at 58.428988ms
                                          Gather#97:
                                          Gather#97 step 1/2 (+0s): 9.992µs self time
                                          Gather#97 step 2/2 (+9.992µs): return nil
                                          Gather#97 ends at 58.43898ms
                                      Gather#108 step 3/6 (+3.331µs): 293ns self time
                                      Gather#108 step 4/6 (+3.624µs): scatter:
                                        Task#101: pool=0
                                        Task#101 step 1/2 (+0s): 100.111µs self time
                                        Task#101 step 2/2 (+100.111µs): return nil
                                        Task#101 ends at 203.681µs
                                          Gather#101:
                                          Gather#101 step 1/6 (+0s): 13ns self time
                                          Gather#101 step 2/6 (+13ns): subjob:
                                            Plan#9: pathCount=2 taskCount=6 maxPathDuration=319.196µs
                                            Plan#9 step 1/3 (+0s): scatter:
                                              Task#106: pool=0
                                              Task#106 step 1/2 (+0s): 99.996µs self time
                                              Task#106 step 2/2 (+99.996µs): return nil
                                              Task#106 ends at 99.996µs
                                                Gather#106:
                                                Gather#106 step 1/4 (+0s): 4.482µs self time
                                                Gather#106 step 2/4 (+4.482µs): scatter:
                                                  Task#104: pool=0
                                                  Task#104 step 1/2 (+0s): 99.819µs self time
                                                  Task#104 step 2/2 (+99.819µs): return nil
                                                  Task#104 ends at 204.297µs
                                                    Gather#104:
                                                    Gather#104 step 1/4 (+0s): 4.903µs self time
                                                    Gather#104 step 2/4 (+4.903µs): scatter:
                                                      Task#102: pool=0
                                                      Task#102 step 1/2 (+0s): 100.001µs self time
                                                      Task#102 step 2/2 (+100.001µs): return nil
                                                      Task#102 ends at 309.201µs
                                                        Gather#102:
                                                        Gather#102 step 1/2 (+0s): 9.995µs self time
                                                        Gather#102 step 2/2 (+9.995µs): return nil
                                                        Gather#102 ends at 319.196µs
                                                    Gather#104 step 3/4 (+4.903µs): 4.904µs self time
                                                    Gather#104 step 4/4 (+9.807µs): return nil
                                                    Gather#104 ends at 214.104µs
                                                Gather#106 step 3/4 (+4.482µs): 4.484µs self time
                                                Gather#106 step 4/4 (+8.966µs): return nil
                                                Gather#106 ends at 108.962µs
                                            Plan#9 step 2/3 (+0s): scatter:
                                              Task#107: pool=0
                                              Task#107 step 1/2 (+0s): 99.999µs self time
                                              Task#107 step 2/2 (+99.999µs): return nil
                                              Task#107 ends at 99.999µs
                                                Gather#107:
                                                Gather#107 step 1/4 (+0s): 5.117µs self time
                                                Gather#107 step 2/4 (+5.117µs): scatter:
                                                  Task#105: pool=0
                                                  Task#105 step 1/2 (+0s): 100.02µs self time
                                                  Task#105 step 2/2 (+100.02µs): return nil
                                                  Task#105 ends at 205.136µs
                                                    Gather#105:
                                                    Gather#105 step 1/4 (+0s): 5.293µs self time
                                                    Gather#105 step 2/4 (+5.293µs): scatter:
                                                      Task#103: pool=0
                                                      Task#103 step 1/2 (+0s): 99.82µs self time
                                                      Task#103 step 2/2 (+99.82µs): return nil
                                                      Task#103 ends at 310.249µs
                                                        Gather#103:
                                                        Gather#103 step 1/2 (+0s): 2.701µs self time
                                                        Gather#103 step 2/2 (+2.701µs): return error
                                                        Gather#103 ends at 312.95µs
                                                    Gather#105 step 3/4 (+5.293µs): 5.336µs self time
                                                    Gather#105 step 4/4 (+10.629µs): return nil
                                                    Gather#105 ends at 215.765µs
                                                Gather#107 step 3/4 (+5.117µs): 4.88µs self time
                                                Gather#107 step 4/4 (+9.997µs): return nil
                                                Gather#107 ends at 109.996µs
                                            Plan#9 step 3/3 (+0s): ends at 319.196µs
                                          Gather#101 step 3/6 (+319.209µs): 78ns self time
                                          Gather#101 step 4/6 (+319.287µs): scatter:
                                            Task#99: pool=0
                                            Task#99 step 1/2 (+0s): 83.691µs self time
                                            Task#99 step 2/2 (+83.691µs): return nil
                                            Task#99 ends at 606.659µs
                                              Gather#99:
                                              Gather#99 step 1/4 (+0s): 4.916µs self time
                                              Gather#99 step 2/4 (+4.916µs): scatter:
                                                Task#98: pool=0
                                                Task#98 step 1/2 (+0s): 99.697µs self time
                                                Task#98 step 2/2 (+99.697µs): return nil
                                                Task#98 ends at 711.272µs
                                                  Gather#98:
                                                  Gather#98 step 1/4 (+0s): 4.238µs self time
                                                  Gather#98 step 2/4 (+4.238µs): scatter:
                                                    Task#96: pool=0
                                                    Task#96 step 1/2 (+0s): 105.04µs self time
                                                    Task#96 step 2/2 (+105.04µs): return nil
                                                    Task#96 ends at 820.55µs
                                                      Gather#96:
                                                      Gather#96 step 1/2 (+0s): 9.971µs self time
                                                      Gather#96 step 2/2 (+9.971µs): return nil
                                                      Gather#96 ends at 830.521µs
                                                  Gather#98 step 3/4 (+4.238µs): 6.759µs self time
                                                  Gather#98 step 4/4 (+10.997µs): return nil
                                                  Gather#98 ends at 722.269µs
                                              Gather#99 step 3/4 (+4.916µs): 5.083µs self time
                                              Gather#99 step 4/4 (+9.999µs): return nil
                                              Gather#99 ends at 616.658µs
                                          Gather#101 step 5/6 (+319.287µs): 468ns self time
                                          Gather#101 step 6/6 (+319.755µs): return nil
                                          Gather#101 ends at 523.436µs
                                      Gather#108 step 5/6 (+3.624µs): 6.375µs self time
                                      Gather#108 step 6/6 (+9.999µs): return nil
                                      Gather#108 ends at 109.945µs
                                  Plan#8 step 3/3 (+0s): ends at 58.43898ms
                                Task#94 step 3/4 (+58.489703ms): 50.729µs self time
                                Task#94 step 4/4 (+58.540432ms): return nil
                                Task#94 ends at 264.136676ms
                                  Gather#94:
                                  Gather#94 step 1/2 (+0s): 9.205µs self time
                                  Gather#94 step 2/2 (+9.205µs): return nil
                                  Gather#94 ends at 264.145881ms
                              Gather#193 step 7/8 (+12.591µs): 4.206µs self time
                              Gather#193 step 8/8 (+16.797µs): return nil
                              Gather#193 ends at 205.60045ms
                          Gather#195 step 5/6 (+100.627335ms): 3.803µs self time
                          Gather#195 step 6/6 (+100.631138ms): return nil
                          Gather#195 ends at 205.487461ms
                      Gather#310 step 9/14 (+3.374µs): 985ns self time
                      Gather#310 step 10/14 (+4.359µs): scatter:
                        Task#187: pool=0
                        Task#187 step 1/2 (+0s): 100.005µs self time
                        Task#187 step 2/2 (+100.005µs): return nil
                        Task#187 ends at 1.910534ms
                          Gather#187:
                          Gather#187 step 1/2 (+0s): 9.971µs self time
                          Gather#187 step 2/2 (+9.971µs): return nil
                          Gather#187 ends at 1.920505ms
                      Gather#310 step 11/14 (+4.359µs): 1.026µs self time
                      Gather#310 step 12/14 (+5.385µs): scatter:
                        Task#308: pool=0
                        Task#308 step 1/2 (+0s): 100.393µs self time
                        Task#308 step 2/2 (+100.393µs): return nil
                        Task#308 ends at 1.911948ms
                          Gather#308:
                          Gather#308 step 1/16 (+0s): 1.241µs self time
                          Gather#308 step 2/16 (+1.241µs): scatter:
                            Task#184: pool=0
                            Task#184 step 1/2 (+0s): 99.985µs self time
                            Task#184 step 2/2 (+99.985µs): return nil
                            Task#184 ends at 2.013174ms
                              Gather#184:
                              Gather#184 step 1/2 (+0s): 10.001µs self time
                              Gather#184 step 2/2 (+10.001µs): return nil
                              Gather#184 ends at 2.023175ms
                          Gather#308 step 3/16 (+1.241µs): 1.244µs self time
                          Gather#308 step 4/16 (+2.485µs): scatter:
                            Task#90: pool=0
                            Task#90 step 1/2 (+0s): 99.961µs self time
                            Task#90 step 2/2 (+99.961µs): return error
                            Task#90 ends at 2.014394ms
                              Gather#90:
                              Gather#90 step 1/2 (+0s): 132.181µs self time
                              Gather#90 step 2/2 (+132.181µs): return nil
                              Gather#90 ends at 2.146575ms
                          Gather#308 step 5/16 (+2.485µs): 1.147µs self time
                          Gather#308 step 6/16 (+3.632µs): scatter:
                            Task#89: pool=0
                            Task#89 step 1/2 (+0s): 96.19µs self time
                            Task#89 step 2/2 (+96.19µs): return nil
                            Task#89 ends at 2.01177ms
                              Gather#89:
                              Gather#89 step 1/2 (+0s): 12.114µs self time
                              Gather#89 step 2/2 (+12.114µs): return nil
                              Gather#89 ends at 2.023884ms
                          Gather#308 step 7/16 (+3.632µs): 1.261µs self time
                          Gather#308 step 8/16 (+4.893µs): scatter:
                            Task#93: pool=0
                            Task#93 step 1/2 (+0s): 99.928µs self time
                            Task#93 step 2/2 (+99.928µs): return nil
                            Task#93 ends at 2.016769ms
                              Gather#93:
                              Gather#93 step 1/2 (+0s): 9.843µs self time
                              Gather#93 step 2/2 (+9.843µs): return nil
                              Gather#93 ends at 2.026612ms
                          Gather#308 step 9/16 (+4.893µs): 1.262µs self time
                          Gather#308 step 10/16 (+6.155µs): scatter:
                            Task#87: pool=0
                            Task#87 step 1/2 (+0s): 97.823µs self time
                            Task#87 step 2/2 (+97.823µs): return nil
                            Task#87 ends at 2.015926ms
                              Gather#87:
                              Gather#87 step 1/2 (+0s): 9.999µs self time
                              Gather#87 step 2/2 (+9.999µs): return nil
                              Gather#87 ends at 2.025925ms
                          Gather#308 step 11/16 (+6.155µs): 1.754µs self time
                          Gather#308 step 12/16 (+7.909µs): scatter:
                            Task#194: pool=0
                            Task#194 step 1/2 (+0s): 0s self time
                            Task#194 step 2/2 (+0s): return nil
                            Task#194 ends at 1.919857ms
                              Gather#194:
                              Gather#194 step 1/4 (+0s): 7.347µs self time
                              Gather#194 step 2/4 (+7.347µs): scatter:
                                Task#191: pool=0
                                Task#191 step 1/2 (+0s): 99.999µs self time
                                Task#191 step 2/2 (+99.999µs): return nil
                                Task#191 ends at 2.027203ms
                                  Gather#191:
                                  Gather#191 step 1/6 (+0s): 3.301µs self time
                                  Gather#191 step 2/6 (+3.301µs): scatter:
                                    Task#189: pool=0
                                    Task#189 step 1/2 (+0s): 100.005µs self time
                                    Task#189 step 2/2 (+100.005µs): return error
                                    Task#189 ends at 2.130509ms
                                      Gather#189:
                                      Gather#189 step 1/8 (+0s): 2.5µs self time
                                      Gather#189 step 2/8 (+2.5µs): scatter:
                                        Task#92: pool=0
                                        Task#92 step 1/2 (+0s): 79.902689ms self time
                                        Task#92 step 2/2 (+79.902689ms): return nil
                                        Task#92 ends at 82.035698ms
                                          Gather#92:
                                          Gather#92 step 1/2 (+0s): 10.781µs self time
                                          Gather#92 step 2/2 (+10.781µs): return nil
                                          Gather#92 ends at 82.046479ms
                                      Gather#189 step 3/8 (+2.5µs): 2.5µs self time
                                      Gather#189 step 4/8 (+5µs): scatter:
                                        Task#63: pool=0
                                        Task#63 step 1/2 (+0s): 128.587µs self time
                                        Task#63 step 2/2 (+128.587µs): return nil
                                        Task#63 ends at 2.264096ms
                                          Gather#63:
                                          Gather#63 step 1/2 (+0s): 10.317µs self time
                                          Gather#63 step 2/2 (+10.317µs): return nil
                                          Gather#63 ends at 2.274413ms
                                      Gather#189 step 5/8 (+5µs): 2.54µs self time
                                      Gather#189 step 6/8 (+7.54µs): scatter:
                                        Task#86: pool=0
                                        Task#86 step 1/2 (+0s): 100.008µs self time
                                        Task#86 step 2/2 (+100.008µs): return nil
                                        Task#86 ends at 2.238057ms
                                          Gather#86:
                                          Gather#86 step 1/2 (+0s): 10.021µs self time
                                          Gather#86 step 2/2 (+10.021µs): return nil
                                          Gather#86 ends at 2.248078ms
                                      Gather#189 step 7/8 (+7.54µs): 2.453µs self time
                                      Gather#189 step 8/8 (+9.993µs): return nil
                                      Gather#189 ends at 2.140502ms
                                  Gather#191 step 3/6 (+3.301µs): 1.358µs self time
                                  Gather#191 step 4/6 (+4.659µs): scatter:
                                    Task#190: pool=0
                                    Task#190 step 1/2 (+0s): 64.782721ms self time
                                    Task#190 step 2/2 (+64.782721ms): return nil
                                    Task#190 ends at 66.814583ms
                                      Gather#190:
                                      Gather#190 step 1/4 (+0s): 2.173µs self time
                                      Gather#190 step 2/4 (+2.173µs): scatter:
                                        Task#188: pool=0
                                        Task#188 step 1/2 (+0s): 69.280455ms self time
                                        Task#188 step 2/2 (+69.280455ms): return nil
                                        Task#188 ends at 136.097211ms
                                          Gather#188:
                                          Gather#188 step 1/2 (+0s): 10.193µs self time
                                          Gather#188 step 2/2 (+10.193µs): return nil
                                          Gather#188 ends at 136.107404ms
                                      Gather#190 step 3/4 (+2.173µs): 7.81µs self time
                                      Gather#190 step 4/4 (+9.983µs): return nil
                                      Gather#190 ends at 66.824566ms
                                  Gather#191 step 5/6 (+4.659µs): 5.338µs self time
                                  Gather#191 step 6/6 (+9.997µs): return nil
                                  Gather#191 ends at 2.0372ms
                              Gather#194 step 3/4 (+7.347µs): 2.618µs self time
                              Gather#194 step 4/4 (+9.965µs): return nil
                              Gather#194 ends at 1.929822ms
                          Gather#308 step 13/16 (+7.909µs): 1.976µs self time
                          Gather#308 step 14/16 (+9.885µs): scatter:
                            Task#64: pool=0
                            Task#64 step 1/2 (+0s): 100.043µs self time
                            Task#64 step 2/2 (+100.043µs): return nil
                            Task#64 ends at 2.021876ms
                              Gather#64:
                              Gather#64 step 1/4 (+0s): 5.007µs self time
                              Gather#64 step 2/4 (+5.007µs): subjob:
                                Plan#6: pathCount=4 taskCount=14 maxPathDuration=32.026454ms
                                Plan#6 step 1/4 (+0s): scatter:
                                  Task#83: pool=1
                                  Task#83 step 1/2 (+0s): 96.125µs self time
                                  Task#83 step 2/2 (+96.125µs): return nil
                                  Task#83 ends at 96.125µs
                                    Gather#83:
                                    Gather#83 step 1/4 (+0s): 2.657µs self time
                                    Gather#83 step 2/4 (+2.657µs): scatter:
                                      Task#80: pool=2
                                      Task#80 step 1/2 (+0s): 100.007µs self time
                                      Task#80 step 2/2 (+100.007µs): return nil
                                      Task#80 ends at 198.789µs
                                        Gather#80:
                                        Gather#80 step 1/4 (+0s): 3.395µs self time
                                        Gather#80 step 2/4 (+3.395µs): scatter:
                                          Task#72: pool=5
                                          Task#72 step 1/2 (+0s): 100.503µs self time
                                          Task#72 step 2/2 (+100.503µs): return error
                                          Task#72 ends at 302.687µs
                                            Gather#72:
                                            Gather#72 step 1/6 (+0s): 524.076µs self time
                                            Gather#72 step 2/6 (+524.076µs): scatter:
                                              Task#69: pool=1
                                              Task#69 step 1/2 (+0s): 150.719µs self time
                                              Task#69 step 2/2 (+150.719µs): return nil
                                              Task#69 ends at 977.482µs
                                                Gather#69:
                                                Gather#69 step 1/4 (+0s): 5.04µs self time
                                                Gather#69 step 2/4 (+5.04µs): scatter:
                                                  Task#66: pool=0
                                                  Task#66 step 1/2 (+0s): 8.698423ms self time
                                                  Task#66 step 2/2 (+8.698423ms): return nil
                                                  Task#66 ends at 9.680945ms
                                                    Gather#66:
                                                    Gather#66 step 1/2 (+0s): 13.531µs self time
                                                    Gather#66 step 2/2 (+13.531µs): return nil
                                                    Gather#66 ends at 9.694476ms
                                                Gather#69 step 3/4 (+5.04µs): 4.99µs self time
                                                Gather#69 step 4/4 (+10.03µs): return nil
                                                Gather#69 ends at 987.512µs
                                            Gather#72 step 3/6 (+524.076µs): 602.006µs self time
                                            Gather#72 step 4/6 (+1.126082ms): subjob:
                                              Plan#7: pathCount=1 taskCount=6 maxPathDuration=29.995629ms
                                              Plan#7 step 1/2 (+0s): scatter:
                                                Task#78: pool=1
                                                Task#78 step 1/2 (+0s): 101.004µs self time
                                                Task#78 step 2/2 (+101.004µs): return nil
                                                Task#78 ends at 101.004µs
                                                  Gather#78:
                                                  Gather#78 step 1/4 (+0s): 39.147µs self time
                                                  Gather#78 step 2/4 (+39.147µs): scatter:
                                                    Task#77: pool=7
                                                    Task#77 step 1/2 (+0s): 99.998µs self time
                                                    Task#77 step 2/2 (+99.998µs): return nil
                                                    Task#77 ends at 240.149µs
                                                      Gather#77:
                                                      Gather#77 step 1/4 (+0s): 5.709µs self time
                                                      Gather#77 step 2/4 (+5.709µs): scatter:
                                                        Task#76: pool=0
                                                        Task#76 step 1/2 (+0s): 99.996µs self time
                                                        Task#76 step 2/2 (+99.996µs): return nil
                                                        Task#76 ends at 345.854µs
                                                          Gather#76:
                                                          Gather#76 step 1/4 (+0s): 4.992µs self time
                                                          Gather#76 step 2/4 (+4.992µs): scatter:
                                                            Task#75: pool=7
                                                            Task#75 step 1/2 (+0s): 29.429926ms self time
                                                            Task#75 step 2/2 (+29.429926ms): return nil
                                                            Task#75 ends at 29.780772ms
                                                              Gather#75:
                                                              Gather#75 step 1/4 (+0s): 8.442µs self time
                                                              Gather#75 step 2/4 (+8.442µs): scatter:
                                                                Task#74: pool=6
                                                                Task#74 step 1/2 (+0s): 100.86µs self time
                                                                Task#74 step 2/2 (+100.86µs): return nil
                                                                Task#74 ends at 29.890074ms
                                                                  Gather#74:
                                                                  Gather#74 step 1/4 (+0s): 5.608µs self time
                                                                  Gather#74 step 2/4 (+5.608µs): scatter:
                                                                    Task#73: pool=2
                                                                    Task#73 step 1/2 (+0s): 90.096µs self time
                                                                    Task#73 step 2/2 (+90.096µs): return nil
                                                                    Task#73 ends at 29.985778ms
                                                                      Gather#73:
                                                                      Gather#73 step 1/2 (+0s): 9.851µs self time
                                                                      Gather#73 step 2/2 (+9.851µs): return nil
                                                                      Gather#73 ends at 29.995629ms
                                                                  Gather#74 step 3/4 (+5.608µs): 4.391µs self time
                                                                  Gather#74 step 4/4 (+9.999µs): return nil
                                                                  Gather#74 ends at 29.900073ms
                                                              Gather#75 step 3/4 (+8.442µs): 1.642µs self time
                                                              Gather#75 step 4/4 (+10.084µs): return nil
                                                              Gather#75 ends at 29.790856ms
                                                          Gather#76 step 3/4 (+4.992µs): 5.007µs self time
                                                          Gather#76 step 4/4 (+9.999µs): return nil
                                                          Gather#76 ends at 355.853µs
                                                      Gather#77 step 3/4 (+5.709µs): 4.292µs self time
                                                      Gather#77 step 4/4 (+10.001µs): return nil
                                                      Gather#77 ends at 250.15µs
                                                  Gather#78 step 3/4 (+39.147µs): 62.22µs self time
                                                  Gather#78 step 4/4 (+101.367µs): return error
                                                  Gather#78 ends at 202.371µs
                                              Plan#7 step 2/2 (+0s): ends at 29.995629ms
                                            Gather#72 step 5/6 (+31.121711ms): 602.056µs self time
                                            Gather#72 step 6/6 (+31.723767ms): return nil
                                            Gather#72 ends at 32.026454ms
                                        Gather#80 step 3/4 (+3.395µs): 1.309µs self time
                                        Gather#80 step 4/4 (+4.704µs): return nil
                                        Gather#80 ends at 203.493µs
                                    Gather#83 step 3/4 (+2.657µs): 3.191µs self time
                                    Gather#83 step 4/4 (+5.848µs): return nil
                                    Gather#83 ends at 101.973µs
                                Plan#6 step 2/4 (+0s): scatter:
                                  Task#84: pool=1
                                  Task#84 step 1/2 (+0s): 99.746µs self time
                                  Task#84 step 2/2 (+99.746µs): return nil
                                  Task#84 ends at 99.746µs
                                    Gather#84:
                                    Gather#84 step 1/6 (+0s): 3.331µs self time
                                    Gather#84 step 2/6 (+3.331µs): scatter:
                                      Task#79: pool=3
                                      Task#79 step 1/2 (+0s): 97.871µs self time
                                      Task#79 step 2/2 (+97.871µs): return nil
                                      Task#79 ends at 200.948µs
                                        Gather#79:
                                        Gather#79 step 1/4 (+0s): 2.477µs self time
                                        Gather#79 step 2/4 (+2.477µs): scatter:
                                          Task#71: pool=1
                                          Task#71 step 1/2 (+0s): 81.735µs self time
                                          Task#71 step 2/2 (+81.735µs): return nil
                                          Task#71 ends at 285.16µs
                                            Combine#71:
                                            Combine#71 step 1/4 (+0s): 703ns self time
                                            Combine#71 step 2/4 (+703ns): scatter:
                                              Task#70: pool=1
                                              Task#70 step 1/2 (+0s): 101.32µs self time
                                              Task#70 step 2/2 (+101.32µs): return nil
                                              Task#70 ends at 387.183µs
                                                Gather#70:
                                                Gather#70 step 1/4 (+0s): 4.925µs self time
                                                Gather#70 step 2/4 (+4.925µs): scatter:
                                                  Task#68: pool=4
                                                  Task#68 step 1/2 (+0s): 76.759µs self time
                                                  Task#68 step 2/2 (+76.759µs): return nil
                                                  Task#68 ends at 468.867µs
                                                    Combine#68:
                                                    Combine#68 step 1/2 (+0s): 10.061µs self time
                                                    Combine#68 step 2/2 (+10.061µs): return nil
                                                    Combine#68 ends at 478.928µs
                                                Gather#70 step 3/4 (+4.925µs): 4.929µs self time
                                                Gather#70 step 4/4 (+9.854µs): return nil
                                                Gather#70 ends at 397.037µs
                                            Combine#71 step 3/4 (+703ns): 9.307µs self time
                                            Combine#71 step 4/4 (+10.01µs): return nil
                                            Combine#71 ends at 295.17µs
                                        Gather#79 step 3/4 (+2.477µs): 2.48µs self time
                                        Gather#79 step 4/4 (+4.957µs): return nil
                                        Gather#79 ends at 205.905µs
                                    Gather#84 step 3/6 (+3.331µs): 3.282µs self time
                                    Gather#84 step 4/6 (+6.613µs): scatter:
                                      Task#81: pool=0
                                      Task#81 step 1/2 (+0s): 99.144µs self time
                                      Task#81 step 2/2 (+99.144µs): return error
                                      Task#81 ends at 205.503µs
                                        Gather#81:
                                        Gather#81 step 1/4 (+0s): 5.803µs self time
                                        Gather#81 step 2/4 (+5.803µs): scatter:
                                          Task#65: pool=6
                                          Task#65 step 1/2 (+0s): 100.013µs self time
                                          Task#65 step 2/2 (+100.013µs): return error
                                          Task#65 ends at 311.319µs
                                            Gather#65:
                                            Gather#65 step 1/2 (+0s): 9.994µs self time
                                            Gather#65 step 2/2 (+9.994µs): return nil
                                            Gather#65 ends at 321.313µs
                                        Gather#81 step 3/4 (+5.803µs): 4.226µs self time
                                        Gather#81 step 4/4 (+10.029µs): return nil
                                        Gather#81 ends at 215.532µs
                                    Gather#84 step 5/6 (+6.613µs): 3.385µs self time
                                    Gather#84 step 6/6 (+9.998µs): return nil
                                    Gather#84 ends at 109.744µs
                                Plan#6 step 3/4 (+0s): scatter:
                                  Task#82: pool=3
                                  Task#82 step 1/2 (+0s): 99.969µs self time
                                  Task#82 step 2/2 (+99.969µs): return nil
                                  Task#82 ends at 99.969µs
                                    Gather#82:
                                    Gather#82 step 1/4 (+0s): 4.896µs self time
                                    Gather#82 step 2/4 (+4.896µs): scatter:
                                      Task#67: pool=1
                                      Task#67 step 1/2 (+0s): 99.956µs self time
                                      Task#67 step 2/2 (+99.956µs): return nil
                                      Task#67 ends at 204.821µs
                                        Gather#67:
                                        Gather#67 step 1/2 (+0s): 9.127µs self time
                                        Gather#67 step 2/2 (+9.127µs): return nil
                                        Gather#67 ends at 213.948µs
                                    Gather#82 step 3/4 (+4.896µs): 5.125µs self time
                                    Gather#82 step 4/4 (+10.021µs): return nil
                                    Gather#82 ends at 109.99µs
                                Plan#6 step 4/4 (+0s): ends at 32.026454ms
                              Gather#64 step 3/4 (+32.031461ms): 4.995µs self time
                              Gather#64 step 4/4 (+32.036456ms): return nil
                              Gather#64 ends at 34.058332ms
                          Gather#308 step 15/16 (+9.885µs): 56ns self time
                          Gather#308 step 16/16 (+9.941µs): return nil
                          Gather#308 ends at 1.921889ms
                      Gather#310 step 13/14 (+5.385µs): 952ns self time
                      Gather#310 step 14/14 (+6.337µs): return nil
                      Gather#310 ends at 1.812507ms
                  Plan#5 step 3/3 (+0s): ends at 308.360135ms
                Gather#62 step 3/4 (+308.365192ms): 5.003µs self time
                Gather#62 step 4/4 (+308.370195ms): return nil
                Gather#62 ends at 308.702125ms
            Gather#315 step 3/6 (+1.956µs): 1.951µs self time
            Gather#315 step 4/6 (+3.907µs): scatter:
              Task#314: pool=1
              Task#314 step 1/2 (+0s): 100.028µs self time
              Task#314 step 2/2 (+100.028µs): return nil
              Task#314 ends at 334.142µs
                Gather#314:
                Gather#314 step 1/2 (+0s): 10.002µs self time
                Gather#314 step 2/2 (+10.002µs): return nil
                Gather#314 ends at 344.144µs
            Gather#315 step 5/6 (+3.907µs): 1.962µs self time
            Gather#315 step 6/6 (+5.869µs): return nil
            Gather#315 ends at 236.076µs
        Gather#319 step 5/6 (+6.665µs): 3.336µs self time
        Gather#319 step 6/6 (+10.001µs): return nil
        Gather#319 ends at 213.106µs
    Combine#321 step 7/8 (+7.5µs): 2.498µs self time
    Combine#321 step 8/8 (+9.998µs): return nil
    Combine#321 ends at 109.998µs
Plan#0 step 3/3 (+0s): ends at 308.702125ms`

	ranOnce := false
	rapid.Check(t, func(t *rapid.T) {
		if ranOnce {
			chk.Fail("must run only once, use -test.v -rapid.v -rapid.log to see error")
		}
		ranOnce = true
		plan := sim.NewPlan(t, &sim.DefaultConfig)
		require.Equal(t, expected, fmt.Sprintf("%#v", plan))
	})
}
