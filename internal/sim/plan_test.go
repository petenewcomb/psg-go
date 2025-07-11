// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim_test

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/petenewcomb/psg-go/internal/sim"
	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

func TestPlanFormatting(t *testing.T) {
	chk := assert.New(t)

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

	//nolint:lll // must be verbatim
	expected := `Plan#0: pathCount=9 taskCount=16 maxPathDuration=35.186905ms minGatherCount=10 maxGatherCount=34
   TaskPools[0]: TaskPool#0: limit=5
   TaskPools[1]: TaskPool#1: limit=1
   CombinerPools[0]: CombinerPool#0: limit=4
   Combiners[0]: pool=0
   Combiners[1]: pool=0
   Combiners[2]: pool=0
   Combiners[3]: pool=0
   Combiners[4]: pool=0
   Combiners[5]: pool=0
   Combiners[6]: pool=0
   Combiners[7]: pool=0
   Combiners[8]: pool=0
   Combiners[9]: pool=0
   Combiners[10]: pool=0
   Combiners[11]: pool=0
   Combiners[12]: pool=0
   Combiners[13]: pool=0
   Combiners[14]: pool=0
Plan#0 step 1/5 (+0s): scatter:
  Task#132: pool=1
  Task#132 step 1/4 (+0s): 3.373µs self time
  Task#132 step 2/4 (+3.373µs): subjob:
    Plan#6: pathCount=11 taskCount=20 maxPathDuration=17.581331ms minGatherCount=15 maxGatherCount=37
       TaskPools[0]: TaskPool#26: limit=8
       CombinerPools[0]: CombinerPool#20: limit=1
       CombinerPools[1]: CombinerPool#21: limit=7
       CombinerPools[2]: CombinerPool#22: limit=2
       CombinerPools[3]: CombinerPool#23: limit=3
       CombinerPools[4]: CombinerPool#24: limit=2
       CombinerPools[5]: CombinerPool#25: limit=2
       CombinerPools[6]: CombinerPool#26: limit=1
       CombinerPools[7]: CombinerPool#27: limit=8
       Combiners[0]: pool=7
       Combiners[1]: pool=4
       Combiners[2]: pool=2
       Combiners[3]: pool=7
       Combiners[4]: pool=7
    Plan#6 step 1/7 (+0s): scatter:
      Task#134: pool=0
      Task#134 step 1/2 (+0s): 9.996µs self time
      Task#134 step 2/2 (+9.996µs): return nil
      Task#134 ends at 9.996µs
        Gather#134: index=1
        Gather#134 step 1/2 (+0s): 999ns self time
        Gather#134 step 2/2 (+999ns): return nil
        Gather#134 ends at 10.995µs
    Plan#6 step 2/7 (+0s): scatter:
      Task#205: pool=0
      Task#205 step 1/4 (+0s): 5.005µs self time
      Task#205 step 2/4 (+5.005µs): subjob:
        Plan#10: pathCount=7 taskCount=12 maxPathDuration=10.35142ms minGatherCount=11 maxGatherCount=13
           TaskPools[0]: TaskPool#36: limit=4
           TaskPools[1]: TaskPool#37: limit=1
           CombinerPools[0]: CombinerPool#47: limit=2
           CombinerPools[1]: CombinerPool#48: limit=1
           Combiners[0]: pool=1
           Combiners[1]: pool=0
           Combiners[2]: pool=0
           Combiners[3]: pool=0
           Combiners[4]: pool=1
           Combiners[5]: pool=0
           Combiners[6]: pool=0
           Combiners[7]: pool=1
           Combiners[8]: pool=0
        Plan#10 step 1/4 (+0s): scatter:
          Task#211: pool=1
          Task#211 step 1/2 (+0s): 5.12518ms self time
          Task#211 step 2/2 (+5.12518ms): return error
          Task#211 ends at 5.12518ms
            Gather#211: index=5
            Gather#211 step 1/2 (+0s): 998ns self time
            Gather#211 step 2/2 (+998ns): return nil
            Gather#211 ends at 5.126178ms
        Plan#10 step 2/4 (+0s): scatter:
          Task#217: pool=0
          Task#217 step 1/4 (+0s): 34.047µs self time
          Task#217 step 2/4 (+34.047µs): subjob:
            Plan#11: pathCount=22 taskCount=36 maxPathDuration=10.011782ms minGatherCount=24 maxGatherCount=42
               TaskPools[0]: TaskPool#38: limit=3
               CombinerPools[0]: CombinerPool#49: limit=2
               CombinerPools[1]: CombinerPool#50: limit=5
               CombinerPools[2]: CombinerPool#51: limit=1
               Combiners[0]: pool=0
               Combiners[1]: pool=2
            Plan#11 step 1/7 (+0s): scatter:
              Task#252: pool=0
              Task#252 step 1/2 (+0s): 10.003µs self time
              Task#252 step 2/2 (+10.003µs): return nil
              Task#252 ends at 10.003µs
                Gather#252: index=0
                Gather#252 step 1/8 (+0s): 148ns self time
                Gather#252 step 2/8 (+148ns): scatter:
                  Task#222: pool=0
                  Task#222 step 1/2 (+0s): 10.019µs self time
                  Task#222 step 2/2 (+10.019µs): return nil
                  Task#222 ends at 20.17µs
                    Gather#222: index=1
                    Gather#222 step 1/2 (+0s): 11.027µs self time
                    Gather#222 step 2/2 (+11.027µs): return nil
                    Gather#222 ends at 31.197µs
                Gather#252 step 3/8 (+148ns): 527ns self time
                Gather#252 step 4/8 (+675ns): scatter:
                  Task#249: pool=0
                  Task#249 step 1/2 (+0s): 201.356µs self time
                  Task#249 step 2/2 (+201.356µs): return nil
                  Task#249 ends at 212.034µs
                    Gather#249: index=1
                    Gather#249 step 1/12 (+0s): 286ns self time
                    Gather#249 step 2/12 (+286ns): scatter:
                      Task#246: pool=0
                      Task#246 step 1/2 (+0s): 10.007µs self time
                      Task#246 step 2/2 (+10.007µs): return nil
                      Task#246 ends at 222.327µs
                        Combine#246: index=1 flush=<nil>
                        Combine#246 step 1/6 (+0s): 943ns self time
                        Combine#246 step 2/6 (+943ns): scatter:
                          Task#242: pool=0
                          Task#242 step 1/2 (+0s): 9.375µs self time
                          Task#242 step 2/2 (+9.375µs): return nil
                          Task#242 ends at 232.645µs
                            Gather#242: index=0
                            Gather#242 step 1/10 (+0s): 202ns self time
                            Gather#242 step 2/10 (+202ns): scatter:
                              Task#236: pool=0
                              Task#236 step 1/2 (+0s): 10.003µs self time
                              Task#236 step 2/2 (+10.003µs): return nil
                              Task#236 ends at 242.85µs
                                Combine#236: index=0 flush=<nil>
                                Combine#236 step 1/2 (+0s): 997ns self time
                                Combine#236 step 2/2 (+997ns): return nil
                                Combine#236 ends at 243.847µs
                            Gather#242 step 3/10 (+202ns): 225ns self time
                            Gather#242 step 4/10 (+427ns): scatter:
                              Task#228: pool=0
                              Task#228 step 1/2 (+0s): 9.924µs self time
                              Task#228 step 2/2 (+9.924µs): return nil
                              Task#228 ends at 242.996µs
                                Gather#228: index=1
                                Gather#228 step 1/2 (+0s): 998ns self time
                                Gather#228 step 2/2 (+998ns): return nil
                                Gather#228 ends at 243.994µs
                            Gather#242 step 5/10 (+427ns): 58ns self time
                            Gather#242 step 6/10 (+485ns): scatter:
                              Task#221: pool=0
                              Task#221 step 1/2 (+0s): 10.028µs self time
                              Task#221 step 2/2 (+10.028µs): return nil
                              Task#221 ends at 243.158µs
                                Gather#221: index=1
                                Gather#221 step 1/2 (+0s): 1ms self time
                                Gather#221 step 2/2 (+1ms): return nil
                                Gather#221 ends at 1.243158ms
                            Gather#242 step 7/10 (+485ns): 239ns self time
                            Gather#242 step 8/10 (+724ns): scatter:
                              Task#237: pool=0
                              Task#237 step 1/2 (+0s): 10.034µs self time
                              Task#237 step 2/2 (+10.034µs): return nil
                              Task#237 ends at 243.403µs
                                Gather#237: index=0
                                Gather#237 step 1/2 (+0s): 1µs self time
                                Gather#237 step 2/2 (+1µs): return nil
                                Gather#237 ends at 244.403µs
                            Gather#242 step 9/10 (+724ns): 244ns self time
                            Gather#242 step 10/10 (+968ns): return nil
                            Gather#242 ends at 233.613µs
                        Combine#246 step 3/6 (+943ns): 23ns self time
                        Combine#246 step 4/6 (+966ns): scatter:
                          Task#230: pool=0
                          Task#230 step 1/2 (+0s): 9.605µs self time
                          Task#230 step 2/2 (+9.605µs): return nil
                          Task#230 ends at 232.898µs
                            Gather#230: index=1
                            Gather#230 step 1/2 (+0s): 1.705µs self time
                            Gather#230 step 2/2 (+1.705µs): return nil
                            Gather#230 ends at 234.603µs
                        Combine#246 step 5/6 (+966ns): 29ns self time
                        Combine#246 step 6/6 (+995ns): return nil
                        Combine#246 ends at 223.322µs
                    Gather#249 step 3/12 (+286ns): 475ns self time
                    Gather#249 step 4/12 (+761ns): scatter:
                      Task#232: pool=0
                      Task#232 step 1/2 (+0s): 10.408µs self time
                      Task#232 step 2/2 (+10.408µs): return nil
                      Task#232 ends at 223.203µs
                        Gather#232: index=0
                        Gather#232 step 1/2 (+0s): 999ns self time
                        Gather#232 step 2/2 (+999ns): return nil
                        Gather#232 ends at 224.202µs
                    Gather#249 step 5/12 (+761ns): 53ns self time
                    Gather#249 step 6/12 (+814ns): scatter:
                      Task#234: pool=0
                      Task#234 step 1/2 (+0s): 10.008µs self time
                      Task#234 step 2/2 (+10.008µs): return nil
                      Task#234 ends at 222.856µs
                        Gather#234: index=0
                        Gather#234 step 1/2 (+0s): 222ns self time
                        Gather#234 step 2/2 (+222ns): return nil
                        Gather#234 ends at 223.078µs
                    Gather#249 step 7/12 (+814ns): 98ns self time
                    Gather#249 step 8/12 (+912ns): scatter:
                      Task#231: pool=0
                      Task#231 step 1/2 (+0s): 10.463µs self time
                      Task#231 step 2/2 (+10.463µs): return nil
                      Task#231 ends at 223.409µs
                        Combine#231: index=1 flush=<nil>
                        Combine#231 step 1/2 (+0s): 114.283µs self time
                        Combine#231 step 2/2 (+114.283µs): return nil
                        Combine#231 ends at 337.692µs
                    Gather#249 step 9/12 (+912ns): 13ns self time
                    Gather#249 step 10/12 (+925ns): scatter:
                      Task#245: pool=0
                      Task#245 step 1/2 (+0s): 10.057µs self time
                      Task#245 step 2/2 (+10.057µs): return nil
                      Task#245 ends at 223.016µs
                        Combine#245: index=0 flush=<nil>
                        Combine#245 step 1/4 (+0s): 778ns self time
                        Combine#245 step 2/4 (+778ns): scatter:
                          Task#241: pool=0
                          Task#241 step 1/2 (+0s): 10.977µs self time
                          Task#241 step 2/2 (+10.977µs): return nil
                          Task#241 ends at 234.771µs
                            Gather#241: index=1
                            Gather#241 step 1/4 (+0s): 414ns self time
                            Gather#241 step 2/4 (+414ns): scatter:
                              Task#227: pool=0
                              Task#227 step 1/2 (+0s): 10.003µs self time
                              Task#227 step 2/2 (+10.003µs): return nil
                              Task#227 ends at 245.188µs
                                Gather#227: index=0
                                Gather#227 step 1/2 (+0s): 1ms self time
                                Gather#227 step 2/2 (+1ms): return nil
                                Gather#227 ends at 1.245188ms
                            Gather#241 step 3/4 (+414ns): 169ns self time
                            Gather#241 step 4/4 (+583ns): return nil
                            Gather#241 ends at 235.354µs
                        Combine#245 step 3/4 (+778ns): 1.459µs self time
                        Combine#245 step 4/4 (+2.237µs): return nil
                        Combine#245 ends at 225.253µs
                    Gather#249 step 11/12 (+925ns): 44ns self time
                    Gather#249 step 12/12 (+969ns): return nil
                    Gather#249 ends at 213.003µs
                Gather#252 step 5/8 (+675ns): 26ns self time
                Gather#252 step 6/8 (+701ns): scatter:
                  Task#233: pool=0
                  Task#233 step 1/2 (+0s): 10ms self time
                  Task#233 step 2/2 (+10ms): return nil
                  Task#233 ends at 10.010704ms
                    Combine#233: index=0 flush=<nil>
                    Combine#233 step 1/2 (+0s): 1.078µs self time
                    Combine#233 step 2/2 (+1.078µs): return nil
                    Combine#233 ends at 10.011782ms
                Gather#252 step 7/8 (+701ns): 39ns self time
                Gather#252 step 8/8 (+740ns): return nil
                Gather#252 ends at 10.743µs
            Plan#11 step 2/7 (+0s): scatter:
              Task#250: pool=0
              Task#250 step 1/2 (+0s): 10.029µs self time
              Task#250 step 2/2 (+10.029µs): return nil
              Task#250 ends at 10.029µs
                Combine#250: index=0 flush=<nil>
                Combine#250 step 1/4 (+0s): 530ns self time
                Combine#250 step 2/4 (+530ns): scatter:
                  Task#220: pool=0
                  Task#220 step 1/2 (+0s): 10.519µs self time
                  Task#220 step 2/2 (+10.519µs): return nil
                  Task#220 ends at 21.078µs
                    Gather#220: index=1
                    Gather#220 step 1/2 (+0s): 979ns self time
                    Gather#220 step 2/2 (+979ns): return nil
                    Gather#220 ends at 22.057µs
                Combine#250 step 3/4 (+530ns): 468ns self time
                Combine#250 step 4/4 (+998ns): return nil
                Combine#250 ends at 11.027µs
            Plan#11 step 3/7 (+0s): scatter:
              Task#239: pool=0
              Task#239 step 1/2 (+0s): 10.003µs self time
              Task#239 step 2/2 (+10.003µs): return nil
              Task#239 ends at 10.003µs
                Gather#239: index=1
                Gather#239 step 1/2 (+0s): 3.739µs self time
                Gather#239 step 2/2 (+3.739µs): return nil
                Gather#239 ends at 13.742µs
            Plan#11 step 4/7 (+0s): scatter:
              Task#253: pool=0
              Task#253 step 1/2 (+0s): 10.011µs self time
              Task#253 step 2/2 (+10.011µs): return nil
              Task#253 ends at 10.011µs
                Gather#253: index=0
                Gather#253 step 1/4 (+0s): 6.324µs self time
                Gather#253 step 2/4 (+6.324µs): scatter:
                  Task#235: pool=0
                  Task#235 step 1/2 (+0s): 9.993µs self time
                  Task#235 step 2/2 (+9.993µs): return nil
                  Task#235 ends at 26.328µs
                    Gather#235: index=0
                    Gather#235 step 1/2 (+0s): 1.344µs self time
                    Gather#235 step 2/2 (+1.344µs): return nil
                    Gather#235 ends at 27.672µs
                Gather#253 step 3/4 (+6.324µs): 9.181µs self time
                Gather#253 step 4/4 (+15.505µs): return nil
                Gather#253 ends at 25.516µs
            Plan#11 step 5/7 (+0s): scatter:
              Task#251: pool=0
              Task#251 step 1/2 (+0s): 10.087µs self time
              Task#251 step 2/2 (+10.087µs): return nil
              Task#251 ends at 10.087µs
                Combine#251: index=0 flush=<nil>
                Combine#251 step 1/10 (+0s): 108ns self time
                Combine#251 step 2/10 (+108ns): scatter:
                  Task#225: pool=0
                  Task#225 step 1/2 (+0s): 46.568µs self time
                  Task#225 step 2/2 (+46.568µs): return nil
                  Task#225 ends at 56.763µs
                    Gather#225: index=0
                    Gather#225 step 1/2 (+0s): 1.001µs self time
                    Gather#225 step 2/2 (+1.001µs): return nil
                    Gather#225 ends at 57.764µs
                Combine#251 step 3/10 (+108ns): 115ns self time
                Combine#251 step 4/10 (+223ns): scatter:
                  Task#247: pool=0
                  Task#247 step 1/2 (+0s): 10.006µs self time
                  Task#247 step 2/2 (+10.006µs): return nil
                  Task#247 ends at 20.316µs
                    Combine#247: index=1 flush=<nil>
                    Combine#247 step 1/6 (+0s): 61ns self time
                    Combine#247 step 2/6 (+61ns): scatter:
                      Task#244: pool=0
                      Task#244 step 1/2 (+0s): 9.984µs self time
                      Task#244 step 2/2 (+9.984µs): return nil
                      Task#244 ends at 30.361µs
                        Gather#244: index=0
                        Gather#244 step 1/4 (+0s): 502ns self time
                        Gather#244 step 2/4 (+502ns): scatter:
                          Task#240: pool=0
                          Task#240 step 1/2 (+0s): 9.505µs self time
                          Task#240 step 2/2 (+9.505µs): return nil
                          Task#240 ends at 40.368µs
                            Combine#240: index=1 flush=<nil>
                            Combine#240 step 1/8 (+0s): 237ns self time
                            Combine#240 step 2/8 (+237ns): scatter:
                              Task#218: pool=0
                              Task#218 step 1/2 (+0s): 10.126µs self time
                              Task#218 step 2/2 (+10.126µs): return nil
                              Task#218 ends at 50.731µs
                                Gather#218: index=0
                                Gather#218 step 1/2 (+0s): 1.002µs self time
                                Gather#218 step 2/2 (+1.002µs): return nil
                                Gather#218 ends at 51.733µs
                            Combine#240 step 3/8 (+237ns): 239ns self time
                            Combine#240 step 4/8 (+476ns): scatter:
                              Task#224: pool=0
                              Task#224 step 1/2 (+0s): 10.024µs self time
                              Task#224 step 2/2 (+10.024µs): return nil
                              Task#224 ends at 50.868µs
                                Gather#224: index=0
                                Gather#224 step 1/2 (+0s): 995ns self time
                                Gather#224 step 2/2 (+995ns): return nil
                                Gather#224 ends at 51.863µs
                            Combine#240 step 5/8 (+476ns): 234ns self time
                            Combine#240 step 6/8 (+710ns): scatter:
                              Task#238: pool=0
                              Task#238 step 1/2 (+0s): 7.04µs self time
                              Task#238 step 2/2 (+7.04µs): return nil
                              Task#238 ends at 48.118µs
                                Gather#238: index=0
                                Gather#238 step 1/2 (+0s): 1.027µs self time
                                Gather#238 step 2/2 (+1.027µs): return nil
                                Gather#238 ends at 49.145µs
                            Combine#240 step 7/8 (+710ns): 236ns self time
                            Combine#240 step 8/8 (+946ns): return nil
                            Combine#240 ends at 41.314µs
                        Gather#244 step 3/4 (+502ns): 498ns self time
                        Gather#244 step 4/4 (+1µs): return nil
                        Gather#244 ends at 31.361µs
                    Combine#247 step 3/6 (+61ns): 50ns self time
                    Combine#247 step 4/6 (+111ns): scatter:
                      Task#243: pool=0
                      Task#243 step 1/2 (+0s): 770.625µs self time
                      Task#243 step 2/2 (+770.625µs): return nil
                      Task#243 ends at 791.052µs
                        Combine#243: index=1 flush=<nil>
                        Combine#243 step 1/4 (+0s): 510ns self time
                        Combine#243 step 2/4 (+510ns): scatter:
                          Task#223: pool=0
                          Task#223 step 1/2 (+0s): 9.381µs self time
                          Task#223 step 2/2 (+9.381µs): return nil
                          Task#223 ends at 800.943µs
                            Gather#223: index=1
                            Gather#223 step 1/2 (+0s): 1.014µs self time
                            Gather#223 step 2/2 (+1.014µs): return nil
                            Gather#223 ends at 801.957µs
                        Combine#243 step 3/4 (+510ns): 513ns self time
                        Combine#243 step 4/4 (+1.023µs): return error
                        Combine#243 ends at 792.075µs
                    Combine#247 step 5/6 (+111ns): 77ns self time
                    Combine#247 step 6/6 (+188ns): return nil
                    Combine#247 ends at 20.504µs
                Combine#251 step 5/10 (+223ns): 6ns self time
                Combine#251 step 6/10 (+229ns): scatter:
                  Task#248: pool=0
                  Task#248 step 1/2 (+0s): 10.003µs self time
                  Task#248 step 2/2 (+10.003µs): return nil
                  Task#248 ends at 20.319µs
                    Gather#248: index=0
                    Gather#248 step 1/4 (+0s): 173ns self time
                    Gather#248 step 2/4 (+173ns): scatter:
                      Task#219: pool=0
                      Task#219 step 1/2 (+0s): 9.992µs self time
                      Task#219 step 2/2 (+9.992µs): return nil
                      Task#219 ends at 30.484µs
                        Gather#219: index=0
                        Gather#219 step 1/2 (+0s): 1.082µs self time
                        Gather#219 step 2/2 (+1.082µs): return nil
                        Gather#219 ends at 31.566µs
                    Gather#248 step 3/4 (+173ns): 367ns self time
                    Gather#248 step 4/4 (+540ns): return nil
                    Gather#248 ends at 20.859µs
                Combine#251 step 7/10 (+229ns): 190ns self time
                Combine#251 step 8/10 (+419ns): scatter:
                  Task#229: pool=0
                  Task#229 step 1/2 (+0s): 13.715µs self time
                  Task#229 step 2/2 (+13.715µs): return nil
                  Task#229 ends at 24.221µs
                    Combine#229: index=0 flush=<nil>
                    Combine#229 step 1/2 (+0s): 589.965µs self time
                    Combine#229 step 2/2 (+589.965µs): return nil
                    Combine#229 ends at 614.186µs
                Combine#251 step 9/10 (+419ns): 189ns self time
                Combine#251 step 10/10 (+608ns): return nil
                Combine#251 ends at 10.695µs
            Plan#11 step 6/7 (+0s): scatter:
              Task#226: pool=0
              Task#226 step 1/2 (+0s): 5.352146ms self time
              Task#226 step 2/2 (+5.352146ms): return nil
              Task#226 ends at 5.352146ms
                Combine#226: index=1 flush=<nil>
                Combine#226 step 1/2 (+0s): 212ns self time
                Combine#226 step 2/2 (+212ns): return nil
                Combine#226 ends at 5.352358ms
            Plan#11 step 7/7 (+0s): ends at 10.011782ms
          Task#217 step 3/4 (+10.045829ms): 33.17µs self time
          Task#217 step 4/4 (+10.078999ms): return nil
          Task#217 ends at 10.078999ms
            Gather#217: index=8
            Gather#217 step 1/4 (+0s): 499ns self time
            Gather#217 step 2/4 (+499ns): scatter:
              Task#216: pool=1
              Task#216 step 1/2 (+0s): 10.496µs self time
              Task#216 step 2/2 (+10.496µs): return nil
              Task#216 ends at 10.089994ms
                Gather#216: index=8
                Gather#216 step 1/8 (+0s): 57ns self time
                Gather#216 step 2/8 (+57ns): scatter:
                  Task#208: pool=1
                  Task#208 step 1/2 (+0s): 17.454µs self time
                  Task#208 step 2/2 (+17.454µs): return nil
                  Task#208 ends at 10.107505ms
                    Combine#208: index=8 flush=<nil>
                    Combine#208 step 1/2 (+0s): 1.002µs self time
                    Combine#208 step 2/2 (+1.002µs): return nil
                    Combine#208 ends at 10.108507ms
                Gather#216 step 3/8 (+57ns): 233ns self time
                Gather#216 step 4/8 (+290ns): scatter:
                  Task#215: pool=0
                  Task#215 step 1/2 (+0s): 10.003µs self time
                  Task#215 step 2/2 (+10.003µs): return nil
                  Task#215 ends at 10.100287ms
                    Gather#215: index=19
                    Gather#215 step 1/8 (+0s): 261ns self time
                    Gather#215 step 2/8 (+261ns): scatter:
                      Task#213: pool=0
                      Task#213 step 1/2 (+0s): 9.993µs self time
                      Task#213 step 2/2 (+9.993µs): return nil
                      Task#213 ends at 10.110541ms
                        Gather#213: index=18
                        Gather#213 step 1/4 (+0s): 119.636µs self time
                        Gather#213 step 2/4 (+119.636µs): scatter:
                          Task#212: pool=1
                          Task#212 step 1/2 (+0s): 9.523µs self time
                          Task#212 step 2/2 (+9.523µs): return nil
                          Task#212 ends at 10.2397ms
                            Gather#212: index=19
                            Gather#212 step 1/2 (+0s): 821ns self time
                            Gather#212 step 2/2 (+821ns): return nil
                            Gather#212 ends at 10.240521ms
                        Gather#213 step 3/4 (+119.636µs): 121.243µs self time
                        Gather#213 step 4/4 (+240.879µs): return nil
                        Gather#213 ends at 10.35142ms
                    Gather#215 step 3/8 (+261ns): 304ns self time
                    Gather#215 step 4/8 (+565ns): scatter:
                      Task#206: pool=0
                      Task#206 step 1/2 (+0s): 10.089µs self time
                      Task#206 step 2/2 (+10.089µs): return nil
                      Task#206 ends at 10.110941ms
                        Combine#206: index=8 flush=Gather#206
                        Combine#206 step 1/2 (+0s): 1.388µs self time
                        Combine#206 step 2/2 (+1.388µs): return nil
                        Combine#206 ends at 10.112329ms
                          Gather#206: index=8
                          Gather#206 step 1/2 (+0s): 998ns self time
                          Gather#206 step 2/2 (+998ns): return nil
                          Gather#206 ends at 0s
                    Gather#215 step 5/8 (+565ns): 260ns self time
                    Gather#215 step 6/8 (+825ns): scatter:
                      Task#214: pool=0
                      Task#214 step 1/2 (+0s): 10.088µs self time
                      Task#214 step 2/2 (+10.088µs): return nil
                      Task#214 ends at 10.1112ms
                        Gather#214: index=0
                        Gather#214 step 1/4 (+0s): 457ns self time
                        Gather#214 step 2/4 (+457ns): scatter:
                          Task#210: pool=0
                          Task#210 step 1/2 (+0s): 4.842µs self time
                          Task#210 step 2/2 (+4.842µs): return nil
                          Task#210 ends at 10.116499ms
                            Gather#210: index=3
                            Gather#210 step 1/2 (+0s): 999ns self time
                            Gather#210 step 2/2 (+999ns): return nil
                            Gather#210 ends at 10.117498ms
                        Gather#214 step 3/4 (+457ns): 541ns self time
                        Gather#214 step 4/4 (+998ns): return nil
                        Gather#214 ends at 10.112198ms
                    Gather#215 step 7/8 (+825ns): 174ns self time
                    Gather#215 step 8/8 (+999ns): return nil
                    Gather#215 ends at 10.101286ms
                Gather#216 step 5/8 (+290ns): 286ns self time
                Gather#216 step 6/8 (+576ns): scatter:
                  Task#207: pool=0
                  Task#207 step 1/2 (+0s): 5.498µs self time
                  Task#207 step 2/2 (+5.498µs): return nil
                  Task#207 ends at 10.096068ms
                    Gather#207: index=1
                    Gather#207 step 1/2 (+0s): 16.275µs self time
                    Gather#207 step 2/2 (+16.275µs): return nil
                    Gather#207 ends at 10.112343ms
                Gather#216 step 7/8 (+576ns): 180ns self time
                Gather#216 step 8/8 (+756ns): return nil
                Gather#216 ends at 10.09075ms
            Gather#217 step 3/4 (+499ns): 501ns self time
            Gather#217 step 4/4 (+1µs): return nil
            Gather#217 ends at 10.079999ms
        Plan#10 step 3/4 (+0s): scatter:
          Task#209: pool=1
          Task#209 step 1/2 (+0s): 19.852µs self time
          Task#209 step 2/2 (+19.852µs): return nil
          Task#209 ends at 19.852µs
            Gather#209: index=1
            Gather#209 step 1/2 (+0s): 988ns self time
            Gather#209 step 2/2 (+988ns): return error
            Gather#209 ends at 20.84µs
        Plan#10 step 4/4 (+0s): ends at 10.35142ms
      Task#205 step 3/4 (+10.356425ms): 5.004µs self time
      Task#205 step 4/4 (+10.361429ms): return nil
      Task#205 ends at 10.361429ms
        Combine#205: index=2 flush=<nil>
        Combine#205 step 1/2 (+0s): 1.006µs self time
        Combine#205 step 2/2 (+1.006µs): return nil
        Combine#205 ends at 10.362435ms
    Plan#6 step 3/7 (+0s): scatter:
      Task#133: pool=0
      Task#133 step 1/2 (+0s): 9.984µs self time
      Task#133 step 2/2 (+9.984µs): return nil
      Task#133 ends at 9.984µs
        Gather#133: index=0
        Gather#133 step 1/2 (+0s): 468.43µs self time
        Gather#133 step 2/2 (+468.43µs): return nil
        Gather#133 ends at 478.414µs
    Plan#6 step 4/7 (+0s): scatter:
      Task#256: pool=0
      Task#256 step 1/2 (+0s): 238.669µs self time
      Task#256 step 2/2 (+238.669µs): return nil
      Task#256 ends at 238.669µs
        Combine#256: index=0 flush=<nil>
        Combine#256 step 1/2 (+0s): 982ns self time
        Combine#256 step 2/2 (+982ns): return nil
        Combine#256 ends at 239.651µs
    Plan#6 step 5/7 (+0s): scatter:
      Task#264: pool=0
      Task#264 step 1/2 (+0s): 9.996µs self time
      Task#264 step 2/2 (+9.996µs): return nil
      Task#264 ends at 9.996µs
        Combine#264: index=0 flush=<nil>
        Combine#264 step 1/4 (+0s): 544ns self time
        Combine#264 step 2/4 (+544ns): scatter:
          Task#261: pool=0
          Task#261 step 1/2 (+0s): 9.998µs self time
          Task#261 step 2/2 (+9.998µs): return nil
          Task#261 ends at 20.538µs
            Gather#261: index=1
            Gather#261 step 1/4 (+0s): 504ns self time
            Gather#261 step 2/4 (+504ns): scatter:
              Task#258: pool=0
              Task#258 step 1/2 (+0s): 9.986µs self time
              Task#258 step 2/2 (+9.986µs): return nil
              Task#258 ends at 31.028µs
                Gather#258: index=2
                Gather#258 step 1/8 (+0s): 138ns self time
                Gather#258 step 2/8 (+138ns): scatter:
                  Task#257: pool=0
                  Task#257 step 1/2 (+0s): 17.926µs self time
                  Task#257 step 2/2 (+17.926µs): return nil
                  Task#257 ends at 49.092µs
                    Gather#257: index=2
                    Gather#257 step 1/4 (+0s): 523ns self time
                    Gather#257 step 2/4 (+523ns): scatter:
                      Task#255: pool=0
                      Task#255 step 1/2 (+0s): 9.12µs self time
                      Task#255 step 2/2 (+9.12µs): return nil
                      Task#255 ends at 58.735µs
                        Gather#255: index=2
                        Gather#255 step 1/2 (+0s): 1.002µs self time
                        Gather#255 step 2/2 (+1.002µs): return nil
                        Gather#255 ends at 59.737µs
                    Gather#257 step 3/4 (+523ns): 486ns self time
                    Gather#257 step 4/4 (+1.009µs): return error
                    Gather#257 ends at 50.101µs
                Gather#258 step 3/8 (+138ns): 197ns self time
                Gather#258 step 4/8 (+335ns): scatter:
                  Task#137: pool=0
                  Task#137 step 1/2 (+0s): 9.977µs self time
                  Task#137 step 2/2 (+9.977µs): return nil
                  Task#137 ends at 41.34µs
                    Gather#137: index=1
                    Gather#137 step 1/2 (+0s): 24.186µs self time
                    Gather#137 step 2/2 (+24.186µs): return nil
                    Gather#137 ends at 65.526µs
                Gather#258 step 5/8 (+335ns): 275ns self time
                Gather#258 step 6/8 (+610ns): scatter:
                  Task#135: pool=0
                  Task#135 step 1/2 (+0s): 3.364922ms self time
                  Task#135 step 2/2 (+3.364922ms): return nil
                  Task#135 ends at 3.39656ms
                    Gather#135: index=2
                    Gather#135 step 1/2 (+0s): 1.307µs self time
                    Gather#135 step 2/2 (+1.307µs): return nil
                    Gather#135 ends at 3.397867ms
                Gather#258 step 7/8 (+610ns): 143ns self time
                Gather#258 step 8/8 (+753ns): return nil
                Gather#258 ends at 31.781µs
            Gather#261 step 3/4 (+504ns): 509ns self time
            Gather#261 step 4/4 (+1.013µs): return nil
            Gather#261 ends at 21.551µs
        Combine#264 step 3/4 (+544ns): 518ns self time
        Combine#264 step 4/4 (+1.062µs): return nil
        Combine#264 ends at 11.058µs
    Plan#6 step 6/7 (+0s): scatter:
      Task#265: pool=0
      Task#265 step 1/2 (+0s): 6.780539ms self time
      Task#265 step 2/2 (+6.780539ms): return nil
      Task#265 ends at 6.780539ms
        Combine#265: index=1 flush=<nil>
        Combine#265 step 1/10 (+0s): 227ns self time
        Combine#265 step 2/10 (+227ns): scatter:
          Task#262: pool=0
          Task#262 step 1/2 (+0s): 9.998µs self time
          Task#262 step 2/2 (+9.998µs): return nil
          Task#262 ends at 6.790764ms
            Gather#262: index=1
            Gather#262 step 1/4 (+0s): 316.347µs self time
            Gather#262 step 2/4 (+316.347µs): scatter:
              Task#254: pool=0
              Task#254 step 1/2 (+0s): 89.371µs self time
              Task#254 step 2/2 (+89.371µs): return nil
              Task#254 ends at 7.196482ms
                Gather#254: index=1
                Gather#254 step 1/2 (+0s): 0s self time
                Gather#254 step 2/2 (+0s): return nil
                Gather#254 ends at 7.196482ms
            Gather#262 step 3/4 (+316.347µs): 0s self time
            Gather#262 step 4/4 (+316.347µs): return nil
            Gather#262 ends at 7.107111ms
        Combine#265 step 3/10 (+227ns): 80ns self time
        Combine#265 step 4/10 (+307ns): scatter:
          Task#260: pool=0
          Task#260 step 1/2 (+0s): 9.98µs self time
          Task#260 step 2/2 (+9.98µs): return nil
          Task#260 ends at 6.790826ms
            Combine#260: index=2 flush=<nil>
            Combine#260 step 1/4 (+0s): 497ns self time
            Combine#260 step 2/4 (+497ns): scatter:
              Task#136: pool=0
              Task#136 step 1/2 (+0s): 15.38µs self time
              Task#136 step 2/2 (+15.38µs): return nil
              Task#136 ends at 6.806703ms
                Gather#136: index=1
                Gather#136 step 1/2 (+0s): 881ns self time
                Gather#136 step 2/2 (+881ns): return nil
                Gather#136 ends at 6.807584ms
            Combine#260 step 3/4 (+497ns): 499ns self time
            Combine#260 step 4/4 (+996ns): return nil
            Combine#260 ends at 6.791822ms
        Combine#265 step 5/10 (+307ns): 246ns self time
        Combine#265 step 6/10 (+553ns): scatter:
          Task#263: pool=0
          Task#263 step 1/2 (+0s): 10.003µs self time
          Task#263 step 2/2 (+10.003µs): return nil
          Task#263 ends at 6.791095ms
            Gather#263: index=0
            Gather#263 step 1/4 (+0s): 179ns self time
            Gather#263 step 2/4 (+179ns): scatter:
              Task#259: pool=0
              Task#259 step 1/2 (+0s): 10.052µs self time
              Task#259 step 2/2 (+10.052µs): return nil
              Task#259 ends at 6.801326ms
                Gather#259: index=0
                Gather#259 step 1/4 (+0s): 14.149µs self time
                Gather#259 step 2/4 (+14.149µs): scatter:
                  Task#138: pool=0
                  Task#138 step 1/2 (+0s): 14.912µs self time
                  Task#138 step 2/2 (+14.912µs): return nil
                  Task#138 ends at 6.830387ms
                    Gather#138: index=2
                    Gather#138 step 1/2 (+0s): 973ns self time
                    Gather#138 step 2/2 (+973ns): return nil
                    Gather#138 ends at 6.83136ms
                Gather#259 step 3/4 (+14.149µs): 14.161µs self time
                Gather#259 step 4/4 (+28.31µs): return nil
                Gather#259 ends at 6.829636ms
            Gather#263 step 3/4 (+179ns): 11ns self time
            Gather#263 step 4/4 (+190ns): return nil
            Gather#263 ends at 6.791285ms
        Combine#265 step 7/10 (+553ns): 227ns self time
        Combine#265 step 8/10 (+780ns): scatter:
          Task#139: pool=0
          Task#139 step 1/4 (+0s): 4.992µs self time
          Task#139 step 2/4 (+4.992µs): subjob:
            Plan#7: pathCount=14 taskCount=24 maxPathDuration=10.071336ms minGatherCount=20 maxGatherCount=28
               TaskPools[0]: TaskPool#27: limit=2
               TaskPools[1]: TaskPool#28: limit=3
               CombinerPools[0]: CombinerPool#28: limit=2
               Combiners[0]: pool=0
               Combiners[1]: pool=0
               Combiners[2]: pool=0
               Combiners[3]: pool=0
            Plan#7 step 1/2 (+0s): scatter:
              Task#204: pool=0
              Task#204 step 1/2 (+0s): 9.766µs self time
              Task#204 step 2/2 (+9.766µs): return nil
              Task#204 ends at 9.766µs
                Gather#204: index=1
                Gather#204 step 1/18 (+0s): 188ns self time
                Gather#204 step 2/18 (+188ns): scatter:
                  Task#177: pool=1
                  Task#177 step 1/2 (+0s): 9.997µs self time
                  Task#177 step 2/2 (+9.997µs): return nil
                  Task#177 ends at 19.951µs
                    Gather#177: index=0
                    Gather#177 step 1/2 (+0s): 7.484µs self time
                    Gather#177 step 2/2 (+7.484µs): return nil
                    Gather#177 ends at 27.435µs
                Gather#204 step 3/18 (+188ns): 104ns self time
                Gather#204 step 4/18 (+292ns): scatter:
                  Task#142: pool=1
                  Task#142 step 1/2 (+0s): 9.997µs self time
                  Task#142 step 2/2 (+9.997µs): return nil
                  Task#142 ends at 20.055µs
                    Combine#142: index=0 flush=<nil>
                    Combine#142 step 1/2 (+0s): 1.082µs self time
                    Combine#142 step 2/2 (+1.082µs): return nil
                    Combine#142 ends at 21.137µs
                Gather#204 step 5/18 (+292ns): 228ns self time
                Gather#204 step 6/18 (+520ns): scatter:
                  Task#172: pool=0
                  Task#172 step 1/2 (+0s): 9.988µs self time
                  Task#172 step 2/2 (+9.988µs): return nil
                  Task#172 ends at 20.274µs
                    Gather#172: index=1
                    Gather#172 step 1/2 (+0s): 603ns self time
                    Gather#172 step 2/2 (+603ns): return nil
                    Gather#172 ends at 20.877µs
                Gather#204 step 7/18 (+520ns): 78ns self time
                Gather#204 step 8/18 (+598ns): scatter:
                  Task#203: pool=1
                  Task#203 step 1/2 (+0s): 10.006µs self time
                  Task#203 step 2/2 (+10.006µs): return nil
                  Task#203 ends at 20.37µs
                    Gather#203: index=1
                    Gather#203 step 1/4 (+0s): 1.412µs self time
                    Gather#203 step 2/4 (+1.412µs): scatter:
                      Task#201: pool=0
                      Task#201 step 1/2 (+0s): 10.005µs self time
                      Task#201 step 2/2 (+10.005µs): return nil
                      Task#201 ends at 31.787µs
                        Gather#201: index=0
                        Gather#201 step 1/4 (+0s): 491ns self time
                        Gather#201 step 2/4 (+491ns): scatter:
                          Task#180: pool=0
                          Task#180 step 1/2 (+0s): 9.996µs self time
                          Task#180 step 2/2 (+9.996µs): return nil
                          Task#180 ends at 42.274µs
                            Gather#180: index=1
                            Gather#180 step 1/6 (+0s): 541ns self time
                            Gather#180 step 2/6 (+541ns): scatter:
                              Task#143: pool=0
                              Task#143 step 1/2 (+0s): 9.301µs self time
                              Task#143 step 2/2 (+9.301µs): return nil
                              Task#143 ends at 52.116µs
                                Gather#143: index=0
                                Gather#143 step 1/2 (+0s): 986ns self time
                                Gather#143 step 2/2 (+986ns): return nil
                                Gather#143 ends at 53.102µs
                            Gather#180 step 3/6 (+541ns): 212ns self time
                            Gather#180 step 4/6 (+753ns): scatter:
                              Task#144: pool=0
                              Task#144 step 1/2 (+0s): 15.853µs self time
                              Task#144 step 2/2 (+15.853µs): return nil
                              Task#144 ends at 58.88µs
                                Gather#144: index=0
                                Gather#144 step 1/4 (+0s): 850ns self time
                                Gather#144 step 2/4 (+850ns): subjob:
                                  Plan#8: pathCount=14 taskCount=24 maxPathDuration=10.011453ms minGatherCount=19 maxGatherCount=31
                                     TaskPools[0]: TaskPool#29: limit=2
                                     TaskPools[1]: TaskPool#30: limit=5
                                     TaskPools[2]: TaskPool#31: limit=2
                                     TaskPools[3]: TaskPool#32: limit=2
                                     CombinerPools[0]: CombinerPool#29: limit=2
                                     CombinerPools[1]: CombinerPool#30: limit=3
                                     CombinerPools[2]: CombinerPool#31: limit=1
                                     CombinerPools[3]: CombinerPool#32: limit=6
                                     CombinerPools[4]: CombinerPool#33: limit=3
                                     CombinerPools[5]: CombinerPool#34: limit=1
                                     CombinerPools[6]: CombinerPool#35: limit=4
                                     CombinerPools[7]: CombinerPool#36: limit=1
                                     CombinerPools[8]: CombinerPool#37: limit=1
                                     CombinerPools[9]: CombinerPool#38: limit=7
                                     Combiners[0]: pool=2
                                     Combiners[1]: pool=6
                                     Combiners[2]: pool=0
                                     Combiners[3]: pool=8
                                     Combiners[4]: pool=1
                                     Combiners[5]: pool=3
                                     Combiners[6]: pool=1
                                     Combiners[7]: pool=0
                                     Combiners[8]: pool=7
                                     Combiners[9]: pool=7
                                     Combiners[10]: pool=7
                                     Combiners[11]: pool=2
                                  Plan#8 step 1/7 (+0s): scatter:
                                    Task#166: pool=0
                                    Task#166 step 1/2 (+0s): 10.126µs self time
                                    Task#166 step 2/2 (+10.126µs): return nil
                                    Task#166 ends at 10.126µs
                                      Combine#166: index=11 flush=<nil>
                                      Combine#166 step 1/4 (+0s): 530ns self time
                                      Combine#166 step 2/4 (+530ns): scatter:
                                        Task#153: pool=1
                                        Task#153 step 1/2 (+0s): 1.203694ms self time
                                        Task#153 step 2/2 (+1.203694ms): return nil
                                        Task#153 ends at 1.21435ms
                                          Gather#153: index=1
                                          Gather#153 step 1/2 (+0s): 990ns self time
                                          Gather#153 step 2/2 (+990ns): return nil
                                          Gather#153 ends at 1.21534ms
                                      Combine#166 step 3/4 (+530ns): 546ns self time
                                      Combine#166 step 4/4 (+1.076µs): return nil
                                      Combine#166 ends at 11.202µs
                                  Plan#8 step 2/7 (+0s): scatter:
                                    Task#167: pool=3
                                    Task#167 step 1/2 (+0s): 10.136µs self time
                                    Task#167 step 2/2 (+10.136µs): return nil
                                    Task#167 ends at 10.136µs
                                      Gather#167: index=1
                                      Gather#167 step 1/6 (+0s): 326ns self time
                                      Gather#167 step 2/6 (+326ns): scatter:
                                        Task#152: pool=2
                                        Task#152 step 1/2 (+0s): 9.391µs self time
                                        Task#152 step 2/2 (+9.391µs): return nil
                                        Task#152 ends at 19.853µs
                                          Gather#152: index=3
                                          Gather#152 step 1/2 (+0s): 1.045µs self time
                                          Gather#152 step 2/2 (+1.045µs): return nil
                                          Gather#152 ends at 20.898µs
                                      Gather#167 step 3/6 (+326ns): 325ns self time
                                      Gather#167 step 4/6 (+651ns): scatter:
                                        Task#154: pool=2
                                        Task#154 step 1/2 (+0s): 9.875µs self time
                                        Task#154 step 2/2 (+9.875µs): return nil
                                        Task#154 ends at 20.662µs
                                          Combine#154: index=10 flush=Gather#154
                                          Combine#154 step 1/2 (+0s): 999ns self time
                                          Combine#154 step 2/2 (+999ns): return nil
                                          Combine#154 ends at 21.661µs
                                            Gather#154: index=8
                                            Gather#154 step 1/2 (+0s): 1.002µs self time
                                            Gather#154 step 2/2 (+1.002µs): return nil
                                            Gather#154 ends at 0s
                                      Gather#167 step 5/6 (+651ns): 331ns self time
                                      Gather#167 step 6/6 (+982ns): return nil
                                      Gather#167 ends at 11.118µs
                                  Plan#8 step 3/7 (+0s): scatter:
                                    Task#155: pool=1
                                    Task#155 step 1/2 (+0s): 10.006µs self time
                                    Task#155 step 2/2 (+10.006µs): return nil
                                    Task#155 ends at 10.006µs
                                      Gather#155: index=5
                                      Gather#155 step 1/2 (+0s): 1ms self time
                                      Gather#155 step 2/2 (+1ms): return nil
                                      Gather#155 ends at 1.010006ms
                                  Plan#8 step 4/7 (+0s): scatter:
                                    Task#165: pool=2
                                    Task#165 step 1/2 (+0s): 0s self time
                                    Task#165 step 2/2 (+0s): return nil
                                    Task#165 ends at 0s
                                      Gather#165: index=8
                                      Gather#165 step 1/6 (+0s): 675ns self time
                                      Gather#165 step 2/6 (+675ns): scatter:
                                        Task#147: pool=3
                                        Task#147 step 1/2 (+0s): 9.998µs self time
                                        Task#147 step 2/2 (+9.998µs): return nil
                                        Task#147 ends at 10.673µs
                                          Gather#147: index=6
                                          Gather#147 step 1/2 (+0s): 6.829µs self time
                                          Gather#147 step 2/2 (+6.829µs): return nil
                                          Gather#147 ends at 17.502µs
                                      Gather#165 step 3/6 (+675ns): 180ns self time
                                      Gather#165 step 4/6 (+855ns): scatter:
                                        Task#151: pool=1
                                        Task#151 step 1/2 (+0s): 1.073µs self time
                                        Task#151 step 2/2 (+1.073µs): return nil
                                        Task#151 ends at 1.928µs
                                          Gather#151: index=11
                                          Gather#151 step 1/2 (+0s): 987ns self time
                                          Gather#151 step 2/2 (+987ns): return nil
                                          Gather#151 ends at 2.915µs
                                      Gather#165 step 5/6 (+855ns): 144ns self time
                                      Gather#165 step 6/6 (+999ns): return nil
                                      Gather#165 ends at 999ns
                                  Plan#8 step 5/7 (+0s): scatter:
                                    Task#157: pool=3
                                    Task#157 step 1/2 (+0s): 3.768µs self time
                                    Task#157 step 2/2 (+3.768µs): return nil
                                    Task#157 ends at 3.768µs
                                      Gather#157: index=2
                                      Gather#157 step 1/2 (+0s): 1.014µs self time
                                      Gather#157 step 2/2 (+1.014µs): return nil
                                      Gather#157 ends at 4.782µs
                                  Plan#8 step 6/7 (+0s): scatter:
                                    Task#168: pool=0
                                    Task#168 step 1/2 (+0s): 9.965µs self time
                                    Task#168 step 2/2 (+9.965µs): return nil
                                    Task#168 ends at 9.965µs
                                      Combine#168: index=11 flush=<nil>
                                      Combine#168 step 1/6 (+0s): 485ns self time
                                      Combine#168 step 2/6 (+485ns): scatter:
                                        Task#163: pool=0
                                        Task#163 step 1/2 (+0s): 10ms self time
                                        Task#163 step 2/2 (+10ms): return nil
                                        Task#163 ends at 10.01045ms
                                          Gather#163: index=8
                                          Gather#163 step 1/4 (+0s): 3ns self time
                                          Gather#163 step 2/4 (+3ns): scatter:
                                            Task#149: pool=3
                                            Task#149 step 1/2 (+0s): 0s self time
                                            Task#149 step 2/2 (+0s): return nil
                                            Task#149 ends at 10.010453ms
                                              Gather#149: index=10
                                              Gather#149 step 1/2 (+0s): 1µs self time
                                              Gather#149 step 2/2 (+1µs): return nil
                                              Gather#149 ends at 10.011453ms
                                          Gather#163 step 3/4 (+3ns): 21ns self time
                                          Gather#163 step 4/4 (+24ns): return nil
                                          Gather#163 ends at 10.010474ms
                                      Combine#168 step 3/6 (+485ns): 0s self time
                                      Combine#168 step 4/6 (+485ns): scatter:
                                        Task#164: pool=2
                                        Task#164 step 1/2 (+0s): 9.999µs self time
                                        Task#164 step 2/2 (+9.999µs): return nil
                                        Task#164 ends at 20.449µs
                                          Combine#164: index=3 flush=Gather#164
                                          Combine#164 step 1/8 (+0s): 161ns self time
                                          Combine#164 step 2/8 (+161ns): scatter:
                                            Task#162: pool=3
                                            Task#162 step 1/2 (+0s): 185.775µs self time
                                            Task#162 step 2/2 (+185.775µs): return nil
                                            Task#162 ends at 206.385µs
                                              Combine#162: index=11 flush=<nil>
                                              Combine#162 step 1/6 (+0s): 295ns self time
                                              Combine#162 step 2/6 (+295ns): scatter:
                                                Task#159: pool=2
                                                Task#159 step 1/2 (+0s): 37.921µs self time
                                                Task#159 step 2/2 (+37.921µs): return nil
                                                Task#159 ends at 244.601µs
                                                  Gather#159: index=1
                                                  Gather#159 step 1/4 (+0s): 1.433µs self time
                                                  Gather#159 step 2/4 (+1.433µs): scatter:
                                                    Task#145: pool=1
                                                    Task#145 step 1/2 (+0s): 8.513µs self time
                                                    Task#145 step 2/2 (+8.513µs): return nil
                                                    Task#145 ends at 254.547µs
                                                      Gather#145: index=1
                                                      Gather#145 step 1/2 (+0s): 870ns self time
                                                      Gather#145 step 2/2 (+870ns): return nil
                                                      Gather#145 ends at 255.417µs
                                                  Gather#159 step 3/4 (+1.433µs): 1.458µs self time
                                                  Gather#159 step 4/4 (+2.891µs): return nil
                                                  Gather#159 ends at 247.492µs
                                              Combine#162 step 3/6 (+295ns): 245ns self time
                                              Combine#162 step 4/6 (+540ns): scatter:
                                                Task#158: pool=2
                                                Task#158 step 1/2 (+0s): 9.996µs self time
                                                Task#158 step 2/2 (+9.996µs): return nil
                                                Task#158 ends at 216.921µs
                                                  Combine#158: index=5 flush=<nil>
                                                  Combine#158 step 1/2 (+0s): 870.273µs self time
                                                  Combine#158 step 2/2 (+870.273µs): return nil
                                                  Combine#158 ends at 1.087194ms
                                              Combine#162 step 5/6 (+540ns): 115ns self time
                                              Combine#162 step 6/6 (+655ns): return nil
                                              Combine#162 ends at 207.04µs
                                          Combine#164 step 3/8 (+161ns): 319ns self time
                                          Combine#164 step 4/8 (+480ns): scatter:
                                            Task#161: pool=0
                                            Task#161 step 1/2 (+0s): 9.752µs self time
                                            Task#161 step 2/2 (+9.752µs): return nil
                                            Task#161 ends at 30.681µs
                                              Gather#161: index=11
                                              Gather#161 step 1/8 (+0s): 23.126µs self time
                                              Gather#161 step 2/8 (+23.126µs): scatter:
                                                Task#146: pool=1
                                                Task#146 step 1/2 (+0s): 9.806µs self time
                                                Task#146 step 2/2 (+9.806µs): return nil
                                                Task#146 ends at 63.613µs
                                                  Gather#146: index=1
                                                  Gather#146 step 1/2 (+0s): 356ns self time
                                                  Gather#146 step 2/2 (+356ns): return nil
                                                  Gather#146 ends at 63.969µs
                                              Gather#161 step 3/8 (+23.126µs): 23.357µs self time
                                              Gather#161 step 4/8 (+46.483µs): scatter:
                                                Task#148: pool=2
                                                Task#148 step 1/2 (+0s): 10.051µs self time
                                                Task#148 step 2/2 (+10.051µs): return nil
                                                Task#148 ends at 87.215µs
                                                  Gather#148: index=0
                                                  Gather#148 step 1/2 (+0s): 999ns self time
                                                  Gather#148 step 2/2 (+999ns): return nil
                                                  Gather#148 ends at 88.214µs
                                              Gather#161 step 5/8 (+46.483µs): 23.041µs self time
                                              Gather#161 step 6/8 (+69.524µs): scatter:
                                                Task#160: pool=0
                                                Task#160 step 1/2 (+0s): 647ns self time
                                                Task#160 step 2/2 (+647ns): return nil
                                                Task#160 ends at 100.852µs
                                                  Gather#160: index=1
                                                  Gather#160 step 1/4 (+0s): 315ns self time
                                                  Gather#160 step 2/4 (+315ns): scatter:
                                                    Task#156: pool=2
                                                    Task#156 step 1/2 (+0s): 991ns self time
                                                    Task#156 step 2/2 (+991ns): return nil
                                                    Task#156 ends at 102.158µs
                                                      Combine#156: index=6 flush=<nil>
                                                      Combine#156 step 1/2 (+0s): 217ns self time
                                                      Combine#156 step 2/2 (+217ns): return nil
                                                      Combine#156 ends at 102.375µs
                                                  Gather#160 step 3/4 (+315ns): 315ns self time
                                                  Gather#160 step 4/4 (+630ns): return nil
                                                  Gather#160 ends at 101.482µs
                                              Gather#161 step 7/8 (+69.524µs): 23.04µs self time
                                              Gather#161 step 8/8 (+92.564µs): return nil
                                              Gather#161 ends at 123.245µs
                                          Combine#164 step 5/8 (+480ns): 310ns self time
                                          Combine#164 step 6/8 (+790ns): scatter:
                                            Task#150: pool=2
                                            Task#150 step 1/2 (+0s): 10.033µs self time
                                            Task#150 step 2/2 (+10.033µs): return nil
                                            Task#150 ends at 31.272µs
                                              Gather#150: index=11
                                              Gather#150 step 1/2 (+0s): 994ns self time
                                              Gather#150 step 2/2 (+994ns): return nil
                                              Gather#150 ends at 32.266µs
                                          Combine#164 step 7/8 (+790ns): 314ns self time
                                          Combine#164 step 8/8 (+1.104µs): return nil
                                          Combine#164 ends at 21.553µs
                                            Gather#164: index=3
                                            Gather#164 step 1/2 (+0s): 1.448µs self time
                                            Gather#164 step 2/2 (+1.448µs): return nil
                                            Gather#164 ends at 0s
                                      Combine#168 step 5/6 (+485ns): 981ns self time
                                      Combine#168 step 6/6 (+1.466µs): return error
                                      Combine#168 ends at 11.431µs
                                  Plan#8 step 7/7 (+0s): ends at 10.011453ms
                                Gather#144 step 3/4 (+10.012303ms): 153ns self time
                                Gather#144 step 4/4 (+10.012456ms): return nil
                                Gather#144 ends at 10.071336ms
                            Gather#180 step 5/6 (+753ns): 220ns self time
                            Gather#180 step 6/6 (+973ns): return nil
                            Gather#180 ends at 43.247µs
                        Gather#201 step 3/4 (+491ns): 513ns self time
                        Gather#201 step 4/4 (+1.004µs): return nil
                        Gather#201 ends at 32.791µs
                    Gather#203 step 3/4 (+1.412µs): 1.416µs self time
                    Gather#203 step 4/4 (+2.828µs): return nil
                    Gather#203 ends at 23.198µs
                Gather#204 step 9/18 (+598ns): 266ns self time
                Gather#204 step 10/18 (+864ns): scatter:
                  Task#140: pool=0
                  Task#140 step 1/2 (+0s): 9.996µs self time
                  Task#140 step 2/2 (+9.996µs): return nil
                  Task#140 ends at 20.626µs
                    Gather#140: index=1
                    Gather#140 step 1/2 (+0s): 832ns self time
                    Gather#140 step 2/2 (+832ns): return nil
                    Gather#140 ends at 21.458µs
                Gather#204 step 11/18 (+864ns): 24ns self time
                Gather#204 step 12/18 (+888ns): scatter:
                  Task#202: pool=1
                  Task#202 step 1/2 (+0s): 9.998µs self time
                  Task#202 step 2/2 (+9.998µs): return nil
                  Task#202 ends at 20.652µs
                    Gather#202: index=0
                    Gather#202 step 1/10 (+0s): 230ns self time
                    Gather#202 step 2/10 (+230ns): scatter:
                      Task#199: pool=0
                      Task#199 step 1/2 (+0s): 8.512µs self time
                      Task#199 step 2/2 (+8.512µs): return nil
                      Task#199 ends at 29.394µs
                        Gather#199: index=0
                        Gather#199 step 1/8 (+0s): 170ns self time
                        Gather#199 step 2/8 (+170ns): scatter:
                          Task#179: pool=0
                          Task#179 step 1/2 (+0s): 10.447µs self time
                          Task#179 step 2/2 (+10.447µs): return error
                          Task#179 ends at 40.011µs
                            Gather#179: index=0
                            Gather#179 step 1/4 (+0s): 559ns self time
                            Gather#179 step 2/4 (+559ns): scatter:
                              Task#171: pool=1
                              Task#171 step 1/2 (+0s): 8.673321ms self time
                              Task#171 step 2/2 (+8.673321ms): return nil
                              Task#171 ends at 8.713891ms
                                Gather#171: index=0
                                Gather#171 step 1/2 (+0s): 995ns self time
                                Gather#171 step 2/2 (+995ns): return nil
                                Gather#171 ends at 8.714886ms
                            Gather#179 step 3/4 (+559ns): 443ns self time
                            Gather#179 step 4/4 (+1.002µs): return nil
                            Gather#179 ends at 41.013µs
                        Gather#199 step 3/8 (+170ns): 87ns self time
                        Gather#199 step 4/8 (+257ns): scatter:
                          Task#178: pool=1
                          Task#178 step 1/2 (+0s): 9.845µs self time
                          Task#178 step 2/2 (+9.845µs): return nil
                          Task#178 ends at 39.496µs
                            Combine#178: index=3 flush=Gather#178
                            Combine#178 step 1/4 (+0s): 498ns self time
                            Combine#178 step 2/4 (+498ns): scatter:
                              Task#174: pool=1
                              Task#174 step 1/2 (+0s): 10µs self time
                              Task#174 step 2/2 (+10µs): return nil
                              Task#174 ends at 49.994µs
                                Gather#174: index=1
                                Gather#174 step 1/2 (+0s): 1.002µs self time
                                Gather#174 step 2/2 (+1.002µs): return nil
                                Gather#174 ends at 50.996µs
                            Combine#178 step 3/4 (+498ns): 503ns self time
                            Combine#178 step 4/4 (+1.001µs): return nil
                            Combine#178 ends at 40.497µs
                              Gather#178: index=0
                              Gather#178 step 1/2 (+0s): 999ns self time
                              Gather#178 step 2/2 (+999ns): return nil
                              Gather#178 ends at 0s
                        Gather#199 step 5/8 (+257ns): 198ns self time
                        Gather#199 step 6/8 (+455ns): scatter:
                          Task#181: pool=1
                          Task#181 step 1/2 (+0s): 9.992µs self time
                          Task#181 step 2/2 (+9.992µs): return nil
                          Task#181 ends at 39.841µs
                            Combine#181: index=1 flush=<nil>
                            Combine#181 step 1/6 (+0s): 329ns self time
                            Combine#181 step 2/6 (+329ns): scatter:
                              Task#173: pool=0
                              Task#173 step 1/2 (+0s): 7.142µs self time
                              Task#173 step 2/2 (+7.142µs): return nil
                              Task#173 ends at 47.312µs
                                Gather#173: index=0
                                Gather#173 step 1/2 (+0s): 973ns self time
                                Gather#173 step 2/2 (+973ns): return nil
                                Gather#173 ends at 48.285µs
                            Combine#181 step 3/6 (+329ns): 195ns self time
                            Combine#181 step 4/6 (+524ns): subjob:
                              Plan#9: pathCount=7 taskCount=17 maxPathDuration=3.28595ms minGatherCount=10 maxGatherCount=24
                                 TaskPools[0]: TaskPool#33: limit=2
                                 TaskPools[1]: TaskPool#34: limit=3
                                 TaskPools[2]: TaskPool#35: limit=5
                                 CombinerPools[0]: CombinerPool#39: limit=3
                                 CombinerPools[1]: CombinerPool#40: limit=2
                                 CombinerPools[2]: CombinerPool#41: limit=3
                                 CombinerPools[3]: CombinerPool#42: limit=1
                                 CombinerPools[4]: CombinerPool#43: limit=2
                                 CombinerPools[5]: CombinerPool#44: limit=2
                                 CombinerPools[6]: CombinerPool#45: limit=7
                                 CombinerPools[7]: CombinerPool#46: limit=2
                                 Combiners[0]: pool=1
                              Plan#9 step 1/6 (+0s): scatter:
                                Task#195: pool=2
                                Task#195 step 1/2 (+0s): 6.478µs self time
                                Task#195 step 2/2 (+6.478µs): return nil
                                Task#195 ends at 6.478µs
                                  Combine#195: index=0 flush=<nil>
                                  Combine#195 step 1/4 (+0s): 197.495µs self time
                                  Combine#195 step 2/4 (+197.495µs): scatter:
                                    Task#192: pool=2
                                    Task#192 step 1/2 (+0s): 9.562µs self time
                                    Task#192 step 2/2 (+9.562µs): return nil
                                    Task#192 ends at 213.535µs
                                      Combine#192: index=0 flush=<nil>
                                      Combine#192 step 1/6 (+0s): 30ns self time
                                      Combine#192 step 2/6 (+30ns): scatter:
                                        Task#182: pool=2
                                        Task#182 step 1/2 (+0s): 9.982µs self time
                                        Task#182 step 2/2 (+9.982µs): return nil
                                        Task#182 ends at 223.547µs
                                          Gather#182: index=0
                                          Gather#182 step 1/2 (+0s): 111ns self time
                                          Gather#182 step 2/2 (+111ns): return nil
                                          Gather#182 ends at 223.658µs
                                      Combine#192 step 3/6 (+30ns): 323ns self time
                                      Combine#192 step 4/6 (+353ns): scatter:
                                        Task#190: pool=0
                                        Task#190 step 1/2 (+0s): 11.582µs self time
                                        Task#190 step 2/2 (+11.582µs): return nil
                                        Task#190 ends at 225.47µs
                                          Gather#190: index=1
                                          Gather#190 step 1/4 (+0s): 474ns self time
                                          Gather#190 step 2/4 (+474ns): scatter:
                                            Task#189: pool=0
                                            Task#189 step 1/2 (+0s): 10.345µs self time
                                            Task#189 step 2/2 (+10.345µs): return nil
                                            Task#189 ends at 236.289µs
                                              Gather#189: index=0
                                              Gather#189 step 1/4 (+0s): 507ns self time
                                              Gather#189 step 2/4 (+507ns): scatter:
                                                Task#186: pool=0
                                                Task#186 step 1/2 (+0s): 9.609µs self time
                                                Task#186 step 2/2 (+9.609µs): return nil
                                                Task#186 ends at 246.405µs
                                                  Combine#186: index=0 flush=<nil>
                                                  Combine#186 step 1/2 (+0s): 1.003µs self time
                                                  Combine#186 step 2/2 (+1.003µs): return nil
                                                  Combine#186 ends at 247.408µs
                                              Gather#189 step 3/4 (+507ns): 312ns self time
                                              Gather#189 step 4/4 (+819ns): return nil
                                              Gather#189 ends at 237.108µs
                                          Gather#190 step 3/4 (+474ns): 475ns self time
                                          Gather#190 step 4/4 (+949ns): return error
                                          Gather#190 ends at 226.419µs
                                      Combine#192 step 5/6 (+353ns): 126ns self time
                                      Combine#192 step 6/6 (+479ns): return nil
                                      Combine#192 ends at 214.014µs
                                  Combine#195 step 3/4 (+197.495µs): 234.477µs self time
                                  Combine#195 step 4/4 (+431.972µs): return nil
                                  Combine#195 ends at 438.45µs
                              Plan#9 step 2/6 (+0s): scatter:
                                Task#196: pool=0
                                Task#196 step 1/2 (+0s): 9.993µs self time
                                Task#196 step 2/2 (+9.993µs): return nil
                                Task#196 ends at 9.993µs
                                  Combine#196: index=0 flush=<nil>
                                  Combine#196 step 1/4 (+0s): 272ns self time
                                  Combine#196 step 2/4 (+272ns): scatter:
                                    Task#188: pool=0
                                    Task#188 step 1/2 (+0s): 13.028µs self time
                                    Task#188 step 2/2 (+13.028µs): return nil
                                    Task#188 ends at 23.293µs
                                      Gather#188: index=0
                                      Gather#188 step 1/2 (+0s): 823.283µs self time
                                      Gather#188 step 2/2 (+823.283µs): return nil
                                      Gather#188 ends at 846.576µs
                                  Combine#196 step 3/4 (+272ns): 653ns self time
                                  Combine#196 step 4/4 (+925ns): return nil
                                  Combine#196 ends at 10.918µs
                              Plan#9 step 3/6 (+0s): scatter:
                                Task#197: pool=0
                                Task#197 step 1/2 (+0s): 3.252558ms self time
                                Task#197 step 2/2 (+3.252558ms): return nil
                                Task#197 ends at 3.252558ms
                                  Gather#197: index=1
                                  Gather#197 step 1/6 (+0s): 139ns self time
                                  Gather#197 step 2/6 (+139ns): scatter:
                                    Task#183: pool=2
                                    Task#183 step 1/2 (+0s): 10.04µs self time
                                    Task#183 step 2/2 (+10.04µs): return nil
                                    Task#183 ends at 3.262737ms
                                      Gather#183: index=1
                                      Gather#183 step 1/2 (+0s): 2.121µs self time
                                      Gather#183 step 2/2 (+2.121µs): return error
                                      Gather#183 ends at 3.264858ms
                                  Gather#197 step 3/6 (+139ns): 437ns self time
                                  Gather#197 step 4/6 (+576ns): scatter:
                                    Task#194: pool=2
                                    Task#194 step 1/2 (+0s): 10.034µs self time
                                    Task#194 step 2/2 (+10.034µs): return nil
                                    Task#194 ends at 3.263168ms
                                      Combine#194: index=0 flush=<nil>
                                      Combine#194 step 1/4 (+0s): 810ns self time
                                      Combine#194 step 2/4 (+810ns): scatter:
                                        Task#191: pool=0
                                        Task#191 step 1/2 (+0s): 9.999µs self time
                                        Task#191 step 2/2 (+9.999µs): return error
                                        Task#191 ends at 3.273977ms
                                          Gather#191: index=1
                                          Gather#191 step 1/4 (+0s): 800ns self time
                                          Gather#191 step 2/4 (+800ns): scatter:
                                            Task#187: pool=1
                                            Task#187 step 1/2 (+0s): 9.996µs self time
                                            Task#187 step 2/2 (+9.996µs): return nil
                                            Task#187 ends at 3.284773ms
                                              Gather#187: index=0
                                              Gather#187 step 1/2 (+0s): 1.177µs self time
                                              Gather#187 step 2/2 (+1.177µs): return nil
                                              Gather#187 ends at 3.28595ms
                                          Gather#191 step 3/4 (+800ns): 199ns self time
                                          Gather#191 step 4/4 (+999ns): return error
                                          Gather#191 ends at 3.274976ms
                                      Combine#194 step 3/4 (+810ns): 14ns self time
                                      Combine#194 step 4/4 (+824ns): return nil
                                      Combine#194 ends at 3.263992ms
                                  Gather#197 step 5/6 (+576ns): 412ns self time
                                  Gather#197 step 6/6 (+988ns): return nil
                                  Gather#197 ends at 3.253546ms
                              Plan#9 step 4/6 (+0s): scatter:
                                Task#184: pool=2
                                Task#184 step 1/2 (+0s): 9.937µs self time
                                Task#184 step 2/2 (+9.937µs): return nil
                                Task#184 ends at 9.937µs
                                  Combine#184: index=0 flush=<nil>
                                  Combine#184 step 1/2 (+0s): 494.901µs self time
                                  Combine#184 step 2/2 (+494.901µs): return error
                                  Combine#184 ends at 504.838µs
                              Plan#9 step 5/6 (+0s): scatter:
                                Task#198: pool=1
                                Task#198 step 1/2 (+0s): 16.193µs self time
                                Task#198 step 2/2 (+16.193µs): return nil
                                Task#198 ends at 16.193µs
                                  Combine#198: index=0 flush=<nil>
                                  Combine#198 step 1/4 (+0s): 983ns self time
                                  Combine#198 step 2/4 (+983ns): scatter:
                                    Task#193: pool=1
                                    Task#193 step 1/2 (+0s): 9.998µs self time
                                    Task#193 step 2/2 (+9.998µs): return nil
                                    Task#193 ends at 27.174µs
                                      Gather#193: index=1
                                      Gather#193 step 1/4 (+0s): 499.973µs self time
                                      Gather#193 step 2/4 (+499.973µs): scatter:
                                        Task#185: pool=0
                                        Task#185 step 1/2 (+0s): 10.005µs self time
                                        Task#185 step 2/2 (+10.005µs): return nil
                                        Task#185 ends at 537.152µs
                                          Gather#185: index=1
                                          Gather#185 step 1/2 (+0s): 1.003µs self time
                                          Gather#185 step 2/2 (+1.003µs): return nil
                                          Gather#185 ends at 538.155µs
                                      Gather#193 step 3/4 (+499.973µs): 500.027µs self time
                                      Gather#193 step 4/4 (+1ms): return nil
                                      Gather#193 ends at 1.027174ms
                                  Combine#198 step 3/4 (+983ns): 21ns self time
                                  Combine#198 step 4/4 (+1.004µs): return nil
                                  Combine#198 ends at 17.197µs
                              Plan#9 step 6/6 (+0s): ends at 3.28595ms
                            Combine#181 step 5/6 (+3.286474ms): 471ns self time
                            Combine#181 step 6/6 (+3.286945ms): return nil
                            Combine#181 ends at 3.326786ms
                        Gather#199 step 7/8 (+455ns): 196ns self time
                        Gather#199 step 8/8 (+651ns): return nil
                        Gather#199 ends at 30.045µs
                    Gather#202 step 3/10 (+230ns): 177ns self time
                    Gather#202 step 4/10 (+407ns): scatter:
                      Task#200: pool=1
                      Task#200 step 1/2 (+0s): 10.03µs self time
                      Task#200 step 2/2 (+10.03µs): return nil
                      Task#200 ends at 31.089µs
                        Combine#200: index=0 flush=<nil>
                        Combine#200 step 1/4 (+0s): 646ns self time
                        Combine#200 step 2/4 (+646ns): scatter:
                          Task#170: pool=0
                          Task#170 step 1/2 (+0s): 10.024µs self time
                          Task#170 step 2/2 (+10.024µs): return nil
                          Task#170 ends at 41.759µs
                            Gather#170: index=1
                            Gather#170 step 1/2 (+0s): 807ns self time
                            Gather#170 step 2/2 (+807ns): return nil
                            Gather#170 ends at 42.566µs
                        Combine#200 step 3/4 (+646ns): 354ns self time
                        Combine#200 step 4/4 (+1µs): return nil
                        Combine#200 ends at 32.089µs
                    Gather#202 step 5/10 (+407ns): 177ns self time
                    Gather#202 step 6/10 (+584ns): scatter:
                      Task#141: pool=0
                      Task#141 step 1/2 (+0s): 9.995µs self time
                      Task#141 step 2/2 (+9.995µs): return nil
                      Task#141 ends at 31.231µs
                        Gather#141: index=0
                        Gather#141 step 1/2 (+0s): 991ns self time
                        Gather#141 step 2/2 (+991ns): return nil
                        Gather#141 ends at 32.222µs
                    Gather#202 step 7/10 (+584ns): 130ns self time
                    Gather#202 step 8/10 (+714ns): scatter:
                      Task#176: pool=0
                      Task#176 step 1/2 (+0s): 7.595µs self time
                      Task#176 step 2/2 (+7.595µs): return nil
                      Task#176 ends at 28.961µs
                        Gather#176: index=0
                        Gather#176 step 1/2 (+0s): 493ns self time
                        Gather#176 step 2/2 (+493ns): return nil
                        Gather#176 ends at 29.454µs
                    Gather#202 step 9/10 (+714ns): 222ns self time
                    Gather#202 step 10/10 (+936ns): return nil
                    Gather#202 ends at 21.588µs
                Gather#204 step 13/18 (+888ns): 35ns self time
                Gather#204 step 14/18 (+923ns): scatter:
                  Task#175: pool=0
                  Task#175 step 1/2 (+0s): 9.209µs self time
                  Task#175 step 2/2 (+9.209µs): return nil
                  Task#175 ends at 19.898µs
                    Combine#175: index=0 flush=<nil>
                    Combine#175 step 1/2 (+0s): 1.008µs self time
                    Combine#175 step 2/2 (+1.008µs): return nil
                    Combine#175 ends at 20.906µs
                Gather#204 step 15/18 (+923ns): 0s self time
                Gather#204 step 16/18 (+923ns): scatter:
                  Task#169: pool=1
                  Task#169 step 1/2 (+0s): 9.998µs self time
                  Task#169 step 2/2 (+9.998µs): return nil
                  Task#169 ends at 20.687µs
                    Gather#169: index=0
                    Gather#169 step 1/2 (+0s): 994ns self time
                    Gather#169 step 2/2 (+994ns): return nil
                    Gather#169 ends at 21.681µs
                Gather#204 step 17/18 (+923ns): 75ns self time
                Gather#204 step 18/18 (+998ns): return nil
                Gather#204 ends at 10.764µs
            Plan#7 step 2/2 (+0s): ends at 10.071336ms
          Task#139 step 3/4 (+10.076328ms): 5.001µs self time
          Task#139 step 4/4 (+10.081329ms): return nil
          Task#139 ends at 16.862648ms
            Gather#139: index=1
            Gather#139 step 1/2 (+0s): 718.683µs self time
            Gather#139 step 2/2 (+718.683µs): return nil
            Gather#139 ends at 17.581331ms
        Combine#265 step 9/10 (+780ns): 236ns self time
        Combine#265 step 10/10 (+1.016µs): return nil
        Combine#265 ends at 6.781555ms
    Plan#6 step 7/7 (+0s): ends at 17.581331ms
  Task#132 step 3/4 (+17.584704ms): 3.394µs self time
  Task#132 step 4/4 (+17.588098ms): return nil
  Task#132 ends at 17.588098ms
    Gather#132: index=0
    Gather#132 step 1/2 (+0s): 1.04µs self time
    Gather#132 step 2/2 (+1.04µs): return nil
    Gather#132 ends at 17.589138ms
Plan#0 step 2/5 (+0s): scatter:
  Task#1058: pool=1
  Task#1058 step 1/2 (+0s): 9.996µs self time
  Task#1058 step 2/2 (+9.996µs): return nil
  Task#1058 ends at 9.996µs
    Gather#1058: index=4
    Gather#1058 step 1/4 (+0s): 0s self time
    Gather#1058 step 2/4 (+0s): scatter:
      Task#131: pool=0
      Task#131 step 1/2 (+0s): 10.017µs self time
      Task#131 step 2/2 (+10.017µs): return nil
      Task#131 ends at 20.013µs
        Gather#131: index=1
        Gather#131 step 1/2 (+0s): 990ns self time
        Gather#131 step 2/2 (+990ns): return nil
        Gather#131 ends at 21.003µs
    Gather#1058 step 3/4 (+0s): 1.163µs self time
    Gather#1058 step 4/4 (+1.163µs): return nil
    Gather#1058 ends at 11.159µs
Plan#0 step 3/5 (+0s): scatter:
  Task#1057: pool=1
  Task#1057 step 1/2 (+0s): 10.005µs self time
  Task#1057 step 2/2 (+10.005µs): return nil
  Task#1057 ends at 10.005µs
    Combine#1057: index=14 flush=<nil>
    Combine#1057 step 1/4 (+0s): 358ns self time
    Combine#1057 step 2/4 (+358ns): scatter:
      Task#1056: pool=1
      Task#1056 step 1/2 (+0s): 6.754µs self time
      Task#1056 step 2/2 (+6.754µs): return nil
      Task#1056 ends at 17.117µs
        Gather#1056: index=3
        Gather#1056 step 1/12 (+0s): 283ns self time
        Gather#1056 step 2/12 (+283ns): scatter:
          Task#268: pool=0
          Task#268 step 1/2 (+0s): 9.998µs self time
          Task#268 step 2/2 (+9.998µs): return nil
          Task#268 ends at 27.398µs
            Combine#268: index=12 flush=<nil>
            Combine#268 step 1/6 (+0s): 318ns self time
            Combine#268 step 2/6 (+318ns): scatter:
              Task#129: pool=0
              Task#129 step 1/2 (+0s): 10.873µs self time
              Task#129 step 2/2 (+10.873µs): return nil
              Task#129 ends at 38.589µs
                Combine#129: index=2 flush=<nil>
                Combine#129 step 1/2 (+0s): 1.035µs self time
                Combine#129 step 2/2 (+1.035µs): return nil
                Combine#129 ends at 39.624µs
            Combine#268 step 3/6 (+318ns): 323ns self time
            Combine#268 step 4/6 (+641ns): subjob:
              Plan#12: pathCount=18 taskCount=35 maxPathDuration=35.158538ms minGatherCount=30 maxGatherCount=43
                 TaskPools[0]: TaskPool#39: limit=10
                 CombinerPools[0]: CombinerPool#52: limit=2
                 CombinerPools[1]: CombinerPool#53: limit=6
                 CombinerPools[2]: CombinerPool#54: limit=5
                 CombinerPools[3]: CombinerPool#55: limit=2
                 CombinerPools[4]: CombinerPool#56: limit=1
                 CombinerPools[5]: CombinerPool#57: limit=2
                 Combiners[0]: pool=4
                 Combiners[1]: pool=2
              Plan#12 step 1/10 (+0s): scatter:
                Task#1054: pool=0
                Task#1054 step 1/2 (+0s): 12.285µs self time
                Task#1054 step 2/2 (+12.285µs): return nil
                Task#1054 ends at 12.285µs
                  Gather#1054: index=0
                  Gather#1054 step 1/4 (+0s): 500ns self time
                  Gather#1054 step 2/4 (+500ns): scatter:
                    Task#273: pool=0
                    Task#273 step 1/2 (+0s): 3.83688ms self time
                    Task#273 step 2/2 (+3.83688ms): return nil
                    Task#273 ends at 3.849665ms
                      Gather#273: index=1
                      Gather#273 step 1/2 (+0s): 845ns self time
                      Gather#273 step 2/2 (+845ns): return nil
                      Gather#273 ends at 3.85051ms
                  Gather#1054 step 3/4 (+500ns): 502ns self time
                  Gather#1054 step 4/4 (+1.002µs): return nil
                  Gather#1054 ends at 13.287µs
              Plan#12 step 2/10 (+0s): scatter:
                Task#276: pool=0
                Task#276 step 1/2 (+0s): 9.995µs self time
                Task#276 step 2/2 (+9.995µs): return nil
                Task#276 ends at 9.995µs
                  Gather#276: index=2
                  Gather#276 step 1/2 (+0s): 1.063µs self time
                  Gather#276 step 2/2 (+1.063µs): return nil
                  Gather#276 ends at 11.058µs
              Plan#12 step 3/10 (+0s): scatter:
                Task#274: pool=0
                Task#274 step 1/2 (+0s): 9.97µs self time
                Task#274 step 2/2 (+9.97µs): return nil
                Task#274 ends at 9.97µs
                  Gather#274: index=0
                  Gather#274 step 1/2 (+0s): 1.007µs self time
                  Gather#274 step 2/2 (+1.007µs): return error
                  Gather#274 ends at 10.977µs
              Plan#12 step 4/10 (+0s): scatter:
                Task#897: pool=0
                Task#897 step 1/2 (+0s): 10.002µs self time
                Task#897 step 2/2 (+10.002µs): return nil
                Task#897 ends at 10.002µs
                  Gather#897: index=3
                  Gather#897 step 1/6 (+0s): 0s self time
                  Gather#897 step 2/6 (+0s): scatter:
                    Task#703: pool=0
                    Task#703 step 1/2 (+0s): 10.001µs self time
                    Task#703 step 2/2 (+10.001µs): return nil
                    Task#703 ends at 20.003µs
                      Gather#703: index=3
                      Gather#703 step 1/10 (+0s): 10ns self time
                      Gather#703 step 2/10 (+10ns): scatter:
                        Task#281: pool=0
                        Task#281 step 1/2 (+0s): 10.006µs self time
                        Task#281 step 2/2 (+10.006µs): return nil
                        Task#281 ends at 30.019µs
                          Gather#281: index=0
                          Gather#281 step 1/2 (+0s): 248.184µs self time
                          Gather#281 step 2/2 (+248.184µs): return nil
                          Gather#281 ends at 278.203µs
                      Gather#703 step 3/10 (+10ns): 1.017µs self time
                      Gather#703 step 4/10 (+1.027µs): scatter:
                        Task#275: pool=0
                        Task#275 step 1/2 (+0s): 9.995µs self time
                        Task#275 step 2/2 (+9.995µs): return nil
                        Task#275 ends at 31.025µs
                          Gather#275: index=1
                          Gather#275 step 1/2 (+0s): 1.501µs self time
                          Gather#275 step 2/2 (+1.501µs): return nil
                          Gather#275 ends at 32.526µs
                      Gather#703 step 5/10 (+1.027µs): 1ns self time
                      Gather#703 step 6/10 (+1.028µs): scatter:
                        Task#698: pool=0
                        Task#698 step 1/2 (+0s): 9.995µs self time
                        Task#698 step 2/2 (+9.995µs): return nil
                        Task#698 ends at 31.026µs
                          Gather#698: index=1
                          Gather#698 step 1/6 (+0s): 57.454µs self time
                          Gather#698 step 2/6 (+57.454µs): scatter:
                            Task#545: pool=0
                            Task#545 step 1/2 (+0s): 8.389µs self time
                            Task#545 step 2/2 (+8.389µs): return nil
                            Task#545 ends at 96.869µs
                              Gather#545: index=2
                              Gather#545 step 1/4 (+0s): 177ns self time
                              Gather#545 step 2/4 (+177ns): scatter:
                                Task#271: pool=0
                                Task#271 step 1/2 (+0s): 10.001µs self time
                                Task#271 step 2/2 (+10.001µs): return nil
                                Task#271 ends at 107.047µs
                                  Combine#271: index=0 flush=<nil>
                                  Combine#271 step 1/2 (+0s): 338.09µs self time
                                  Combine#271 step 2/2 (+338.09µs): return nil
                                  Combine#271 ends at 445.137µs
                              Gather#545 step 3/4 (+177ns): 181ns self time
                              Gather#545 step 4/4 (+358ns): return nil
                              Gather#545 ends at 97.227µs
                          Gather#698 step 3/6 (+57.454µs): 57.401µs self time
                          Gather#698 step 4/6 (+114.855µs): scatter:
                            Task#287: pool=0
                            Task#287 step 1/2 (+0s): 2.583309ms self time
                            Task#287 step 2/2 (+2.583309ms): return nil
                            Task#287 ends at 2.72919ms
                              Gather#287: index=0
                              Gather#287 step 1/6 (+0s): 332ns self time
                              Gather#287 step 2/6 (+332ns): subjob:
                                Plan#13: pathCount=24 taskCount=34 maxPathDuration=20.05321ms minGatherCount=26 maxGatherCount=46
                                   TaskPools[0]: TaskPool#40: limit=3
                                   TaskPools[1]: TaskPool#41: limit=3
                                   TaskPools[2]: TaskPool#42: limit=6
                                   CombinerPools[0]: CombinerPool#58: limit=8
                                   CombinerPools[1]: CombinerPool#59: limit=3
                                   CombinerPools[2]: CombinerPool#60: limit=2
                                   CombinerPools[3]: CombinerPool#61: limit=2
                                   CombinerPools[4]: CombinerPool#62: limit=10
                                   CombinerPools[5]: CombinerPool#63: limit=7
                                   CombinerPools[6]: CombinerPool#64: limit=1
                                   CombinerPools[7]: CombinerPool#65: limit=2
                                   CombinerPools[8]: CombinerPool#66: limit=2
                                   Combiners[0]: pool=0
                                   Combiners[1]: pool=7
                                   Combiners[2]: pool=6
                                   Combiners[3]: pool=0
                                   Combiners[4]: pool=6
                                   Combiners[5]: pool=0
                                   Combiners[6]: pool=1
                                   Combiners[7]: pool=2
                                   Combiners[8]: pool=8
                                   Combiners[9]: pool=7
                                   Combiners[10]: pool=3
                                Plan#13 step 1/12 (+0s): scatter:
                                  Task#542: pool=0
                                  Task#542 step 1/2 (+0s): 8.939193ms self time
                                  Task#542 step 2/2 (+8.939193ms): return nil
                                  Task#542 ends at 8.939193ms
                                    Combine#542: index=8 flush=<nil>
                                    Combine#542 step 1/4 (+0s): 866ns self time
                                    Combine#542 step 2/4 (+866ns): scatter:
                                      Task#296: pool=2
                                      Task#296 step 1/2 (+0s): 387.41µs self time
                                      Task#296 step 2/2 (+387.41µs): return error
                                      Task#296 ends at 9.327469ms
                                        Gather#296: index=17
                                        Gather#296 step 1/2 (+0s): 32.991µs self time
                                        Gather#296 step 2/2 (+32.991µs): return nil
                                        Gather#296 ends at 9.36046ms
                                    Combine#542 step 3/4 (+866ns): 668ns self time
                                    Combine#542 step 4/4 (+1.534µs): return nil
                                    Combine#542 ends at 8.940727ms
                                Plan#13 step 2/12 (+0s): scatter:
                                  Task#541: pool=2
                                  Task#541 step 1/2 (+0s): 9.998µs self time
                                  Task#541 step 2/2 (+9.998µs): return nil
                                  Task#541 ends at 9.998µs
                                    Combine#541: index=2 flush=<nil>
                                    Combine#541 step 1/14 (+0s): 728ns self time
                                    Combine#541 step 2/14 (+728ns): scatter:
                                      Task#402: pool=2
                                      Task#402 step 1/2 (+0s): 10.02µs self time
                                      Task#402 step 2/2 (+10.02µs): return nil
                                      Task#402 ends at 20.746µs
                                        Combine#402: index=7 flush=<nil>
                                        Combine#402 step 1/4 (+0s): 943ns self time
                                        Combine#402 step 2/4 (+943ns): subjob:
                                          Plan#18: pathCount=17 taskCount=41 maxPathDuration=8.618889ms minGatherCount=28 maxGatherCount=51
                                             TaskPools[0]: TaskPool#56: limit=8
                                             CombinerPools[0]: CombinerPool#93: limit=1
                                             CombinerPools[1]: CombinerPool#94: limit=8
                                             CombinerPools[2]: CombinerPool#95: limit=10
                                             CombinerPools[3]: CombinerPool#96: limit=7
                                             CombinerPools[4]: CombinerPool#97: limit=1
                                             CombinerPools[5]: CombinerPool#98: limit=1
                                             CombinerPools[6]: CombinerPool#99: limit=2
                                             CombinerPools[7]: CombinerPool#100: limit=2
                                             CombinerPools[8]: CombinerPool#101: limit=3
                                             Combiners[0]: pool=4
                                             Combiners[1]: pool=7
                                             Combiners[2]: pool=1
                                             Combiners[3]: pool=6
                                             Combiners[4]: pool=0
                                             Combiners[5]: pool=7
                                             Combiners[6]: pool=0
                                             Combiners[7]: pool=2
                                             Combiners[8]: pool=3
                                             Combiners[9]: pool=2
                                             Combiners[10]: pool=1
                                             Combiners[11]: pool=0
                                             Combiners[12]: pool=6
                                             Combiners[13]: pool=5
                                             Combiners[14]: pool=0
                                          Plan#18 step 1/8 (+0s): scatter:
                                            Task#441: pool=0
                                            Task#441 step 1/2 (+0s): 10.005µs self time
                                            Task#441 step 2/2 (+10.005µs): return error
                                            Task#441 ends at 10.005µs
                                              Gather#441: index=0
                                              Gather#441 step 1/6 (+0s): 193.238µs self time
                                              Gather#441 step 2/6 (+193.238µs): scatter:
                                                Task#437: pool=0
                                                Task#437 step 1/2 (+0s): 10.006µs self time
                                                Task#437 step 2/2 (+10.006µs): return nil
                                                Task#437 ends at 213.249µs
                                                  Combine#437: index=6 flush=<nil>
                                                  Combine#437 step 1/12 (+0s): 109ns self time
                                                  Combine#437 step 2/12 (+109ns): scatter:
                                                    Task#418: pool=0
                                                    Task#418 step 1/2 (+0s): 9.998µs self time
                                                    Task#418 step 2/2 (+9.998µs): return nil
                                                    Task#418 ends at 223.356µs
                                                      Gather#418: index=0
                                                      Gather#418 step 1/2 (+0s): 0s self time
                                                      Gather#418 step 2/2 (+0s): return nil
                                                      Gather#418 ends at 223.356µs
                                                  Combine#437 step 3/12 (+109ns): 738ns self time
                                                  Combine#437 step 4/12 (+847ns): scatter:
                                                    Task#426: pool=0
                                                    Task#426 step 1/2 (+0s): 9.997µs self time
                                                    Task#426 step 2/2 (+9.997µs): return nil
                                                    Task#426 ends at 224.093µs
                                                      Combine#426: index=13 flush=<nil>
                                                      Combine#426 step 1/4 (+0s): 493ns self time
                                                      Combine#426 step 2/4 (+493ns): scatter:
                                                        Task#420: pool=0
                                                        Task#420 step 1/2 (+0s): 9.988µs self time
                                                        Task#420 step 2/2 (+9.988µs): return nil
                                                        Task#420 ends at 234.574µs
                                                          Gather#420: index=0
                                                          Gather#420 step 1/4 (+0s): 61ns self time
                                                          Gather#420 step 2/4 (+61ns): scatter:
                                                            Task#411: pool=0
                                                            Task#411 step 1/2 (+0s): 9.938µs self time
                                                            Task#411 step 2/2 (+9.938µs): return nil
                                                            Task#411 ends at 244.573µs
                                                              Gather#411: index=0
                                                              Gather#411 step 1/2 (+0s): 902ns self time
                                                              Gather#411 step 2/2 (+902ns): return nil
                                                              Gather#411 ends at 245.475µs
                                                          Gather#420 step 3/4 (+61ns): 70ns self time
                                                          Gather#420 step 4/4 (+131ns): return nil
                                                          Gather#420 ends at 234.705µs
                                                      Combine#426 step 3/4 (+493ns): 510ns self time
                                                      Combine#426 step 4/4 (+1.003µs): return nil
                                                      Combine#426 ends at 225.096µs
                                                  Combine#437 step 5/12 (+847ns): 5ns self time
                                                  Combine#437 step 6/12 (+852ns): scatter:
                                                    Task#427: pool=0
                                                    Task#427 step 1/2 (+0s): 8.275µs self time
                                                    Task#427 step 2/2 (+8.275µs): return nil
                                                    Task#427 ends at 222.376µs
                                                      Gather#427: index=0
                                                      Gather#427 step 1/4 (+0s): 672ns self time
                                                      Gather#427 step 2/4 (+672ns): scatter:
                                                        Task#419: pool=0
                                                        Task#419 step 1/2 (+0s): 10.002µs self time
                                                        Task#419 step 2/2 (+10.002µs): return nil
                                                        Task#419 ends at 233.05µs
                                                          Combine#419: index=0 flush=<nil>
                                                          Combine#419 step 1/2 (+0s): 1.001µs self time
                                                          Combine#419 step 2/2 (+1.001µs): return nil
                                                          Combine#419 ends at 234.051µs
                                                      Gather#427 step 3/4 (+672ns): 710ns self time
                                                      Gather#427 step 4/4 (+1.382µs): return nil
                                                      Gather#427 ends at 223.758µs
                                                  Combine#437 step 7/12 (+852ns): 9ns self time
                                                  Combine#437 step 8/12 (+861ns): scatter:
                                                    Task#430: pool=0
                                                    Task#430 step 1/2 (+0s): 10.037µs self time
                                                    Task#430 step 2/2 (+10.037µs): return nil
                                                    Task#430 ends at 224.147µs
                                                      Combine#430: index=3 flush=<nil>
                                                      Combine#430 step 1/4 (+0s): 878ns self time
                                                      Combine#430 step 2/4 (+878ns): scatter:
                                                        Task#422: pool=0
                                                        Task#422 step 1/2 (+0s): 29.285µs self time
                                                        Task#422 step 2/2 (+29.285µs): return nil
                                                        Task#422 ends at 254.31µs
                                                          Gather#422: index=0
                                                          Gather#422 step 1/4 (+0s): 0s self time
                                                          Gather#422 step 2/4 (+0s): scatter:
                                                            Task#410: pool=0
                                                            Task#410 step 1/2 (+0s): 9.997µs self time
                                                            Task#410 step 2/2 (+9.997µs): return nil
                                                            Task#410 ends at 264.307µs
                                                              Combine#410: index=0 flush=<nil>
                                                              Combine#410 step 1/2 (+0s): 2.975µs self time
                                                              Combine#410 step 2/2 (+2.975µs): return nil
                                                              Combine#410 ends at 267.282µs
                                                          Gather#422 step 3/4 (+0s): 0s self time
                                                          Gather#422 step 4/4 (+0s): return nil
                                                          Gather#422 ends at 254.31µs
                                                      Combine#430 step 3/4 (+878ns): 120ns self time
                                                      Combine#430 step 4/4 (+998ns): return nil
                                                      Combine#430 ends at 225.145µs
                                                  Combine#437 step 9/12 (+861ns): 21ns self time
                                                  Combine#437 step 10/12 (+882ns): scatter:
                                                    Task#404: pool=0
                                                    Task#404 step 1/2 (+0s): 9.985µs self time
                                                    Task#404 step 2/2 (+9.985µs): return nil
                                                    Task#404 ends at 224.116µs
                                                      Gather#404: index=0
                                                      Gather#404 step 1/2 (+0s): 920ns self time
                                                      Gather#404 step 2/2 (+920ns): return nil
                                                      Gather#404 ends at 225.036µs
                                                  Combine#437 step 11/12 (+882ns): 0s self time
                                                  Combine#437 step 12/12 (+882ns): return nil
                                                  Combine#437 ends at 214.131µs
                                              Gather#441 step 3/6 (+193.238µs): 193.249µs self time
                                              Gather#441 step 4/6 (+386.487µs): scatter:
                                                Task#413: pool=0
                                                Task#413 step 1/2 (+0s): 9.767µs self time
                                                Task#413 step 2/2 (+9.767µs): return nil
                                                Task#413 ends at 406.259µs
                                                  Combine#413: index=3 flush=<nil>
                                                  Combine#413 step 1/2 (+0s): 1.041µs self time
                                                  Combine#413 step 2/2 (+1.041µs): return nil
                                                  Combine#413 ends at 407.3µs
                                              Gather#441 step 5/6 (+386.487µs): 193.193µs self time
                                              Gather#441 step 6/6 (+579.68µs): return nil
                                              Gather#441 ends at 589.685µs
                                          Plan#18 step 2/8 (+0s): scatter:
                                            Task#442: pool=0
                                            Task#442 step 1/2 (+0s): 10.024µs self time
                                            Task#442 step 2/2 (+10.024µs): return nil
                                            Task#442 ends at 10.024µs
                                              Combine#442: index=0 flush=<nil>
                                              Combine#442 step 1/8 (+0s): 648ns self time
                                              Combine#442 step 2/8 (+648ns): scatter:
                                                Task#436: pool=0
                                                Task#436 step 1/2 (+0s): 10µs self time
                                                Task#436 step 2/2 (+10µs): return nil
                                                Task#436 ends at 20.672µs
                                                  Gather#436: index=0
                                                  Gather#436 step 1/4 (+0s): 504ns self time
                                                  Gather#436 step 2/4 (+504ns): scatter:
                                                    Task#432: pool=0
                                                    Task#432 step 1/2 (+0s): 9.797µs self time
                                                    Task#432 step 2/2 (+9.797µs): return nil
                                                    Task#432 ends at 30.973µs
                                                      Combine#432: index=13 flush=<nil>
                                                      Combine#432 step 1/4 (+0s): 63ns self time
                                                      Combine#432 step 2/4 (+63ns): scatter:
                                                        Task#407: pool=0
                                                        Task#407 step 1/2 (+0s): 9.949µs self time
                                                        Task#407 step 2/2 (+9.949µs): return nil
                                                        Task#407 ends at 40.985µs
                                                          Gather#407: index=0
                                                          Gather#407 step 1/2 (+0s): 1.494µs self time
                                                          Gather#407 step 2/2 (+1.494µs): return nil
                                                          Gather#407 ends at 42.479µs
                                                      Combine#432 step 3/4 (+63ns): 67ns self time
                                                      Combine#432 step 4/4 (+130ns): return nil
                                                      Combine#432 ends at 31.103µs
                                                  Gather#436 step 3/4 (+504ns): 509ns self time
                                                  Gather#436 step 4/4 (+1.013µs): return nil
                                                  Gather#436 ends at 21.685µs
                                              Combine#442 step 3/8 (+648ns): 27ns self time
                                              Combine#442 step 4/8 (+675ns): scatter:
                                                Task#439: pool=0
                                                Task#439 step 1/2 (+0s): 1.556989ms self time
                                                Task#439 step 2/2 (+1.556989ms): return nil
                                                Task#439 ends at 1.567688ms
                                                  Gather#439: index=0
                                                  Gather#439 step 1/4 (+0s): 10ns self time
                                                  Gather#439 step 2/4 (+10ns): scatter:
                                                    Task#429: pool=0
                                                    Task#429 step 1/2 (+0s): 9.999µs self time
                                                    Task#429 step 2/2 (+9.999µs): return error
                                                    Task#429 ends at 1.577697ms
                                                      Gather#429: index=0
                                                      Gather#429 step 1/4 (+0s): 26ns self time
                                                      Gather#429 step 2/4 (+26ns): scatter:
                                                        Task#409: pool=0
                                                        Task#409 step 1/2 (+0s): 707.722µs self time
                                                        Task#409 step 2/2 (+707.722µs): return nil
                                                        Task#409 ends at 2.285445ms
                                                          Gather#409: index=0
                                                          Gather#409 step 1/2 (+0s): 677ns self time
                                                          Gather#409 step 2/2 (+677ns): return nil
                                                          Gather#409 ends at 2.286122ms
                                                      Gather#429 step 3/4 (+26ns): 970ns self time
                                                      Gather#429 step 4/4 (+996ns): return nil
                                                      Gather#429 ends at 1.578693ms
                                                  Gather#439 step 3/4 (+10ns): 38ns self time
                                                  Gather#439 step 4/4 (+48ns): return nil
                                                  Gather#439 ends at 1.567736ms
                                              Combine#442 step 5/8 (+675ns): 36ns self time
                                              Combine#442 step 6/8 (+711ns): scatter:
                                                Task#435: pool=0
                                                Task#435 step 1/2 (+0s): 9.872µs self time
                                                Task#435 step 2/2 (+9.872µs): return nil
                                                Task#435 ends at 20.607µs
                                                  Gather#435: index=0
                                                  Gather#435 step 1/4 (+0s): 1.789µs self time
                                                  Gather#435 step 2/4 (+1.789µs): scatter:
                                                    Task#431: pool=0
                                                    Task#431 step 1/2 (+0s): 9.996µs self time
                                                    Task#431 step 2/2 (+9.996µs): return nil
                                                    Task#431 ends at 32.392µs
                                                      Combine#431: index=2 flush=Gather#431
                                                      Combine#431 step 1/4 (+0s): 276ns self time
                                                      Combine#431 step 2/4 (+276ns): scatter:
                                                        Task#425: pool=0
                                                        Task#425 step 1/2 (+0s): 9.998µs self time
                                                        Task#425 step 2/2 (+9.998µs): return error
                                                        Task#425 ends at 42.666µs
                                                          Gather#425: index=0
                                                          Gather#425 step 1/4 (+0s): 530ns self time
                                                          Gather#425 step 2/4 (+530ns): scatter:
                                                            Task#408: pool=0
                                                            Task#408 step 1/2 (+0s): 93.572µs self time
                                                            Task#408 step 2/2 (+93.572µs): return nil
                                                            Task#408 ends at 136.768µs
                                                              Combine#408: index=2 flush=<nil>
                                                              Combine#408 step 1/2 (+0s): 992ns self time
                                                              Combine#408 step 2/2 (+992ns): return nil
                                                              Combine#408 ends at 137.76µs
                                                          Gather#425 step 3/4 (+530ns): 80ns self time
                                                          Gather#425 step 4/4 (+610ns): return nil
                                                          Gather#425 ends at 43.276µs
                                                      Combine#431 step 3/4 (+276ns): 504ns self time
                                                      Combine#431 step 4/4 (+780ns): return nil
                                                      Combine#431 ends at 33.172µs
                                                        Gather#431: index=0
                                                        Gather#431 step 1/2 (+0s): 953ns self time
                                                        Gather#431 step 2/2 (+953ns): return nil
                                                        Gather#431 ends at 0s
                                                  Gather#435 step 3/4 (+1.789µs): 2.387µs self time
                                                  Gather#435 step 4/4 (+4.176µs): return nil
                                                  Gather#435 ends at 24.783µs
                                              Combine#442 step 7/8 (+711ns): 36ns self time
                                              Combine#442 step 8/8 (+747ns): return nil
                                              Combine#442 ends at 10.771µs
                                          Plan#18 step 3/8 (+0s): scatter:
                                            Task#443: pool=0
                                            Task#443 step 1/2 (+0s): 10.015µs self time
                                            Task#443 step 2/2 (+10.015µs): return nil
                                            Task#443 ends at 10.015µs
                                              Gather#443: index=0
                                              Gather#443 step 1/6 (+0s): 262ns self time
                                              Gather#443 step 2/6 (+262ns): scatter:
                                                Task#434: pool=0
                                                Task#434 step 1/2 (+0s): 10.002µs self time
                                                Task#434 step 2/2 (+10.002µs): return nil
                                                Task#434 ends at 20.279µs
                                                  Gather#434: index=0
                                                  Gather#434 step 1/4 (+0s): 6.937µs self time
                                                  Gather#434 step 2/4 (+6.937µs): scatter:
                                                    Task#428: pool=0
                                                    Task#428 step 1/2 (+0s): 10µs self time
                                                    Task#428 step 2/2 (+10µs): return nil
                                                    Task#428 ends at 37.216µs
                                                      Combine#428: index=1 flush=<nil>
                                                      Combine#428 step 1/4 (+0s): 159ns self time
                                                      Combine#428 step 2/4 (+159ns): scatter:
                                                        Task#424: pool=0
                                                        Task#424 step 1/2 (+0s): 3.277µs self time
                                                        Task#424 step 2/2 (+3.277µs): return nil
                                                        Task#424 ends at 40.652µs
                                                          Gather#424: index=0
                                                          Gather#424 step 1/4 (+0s): 468ns self time
                                                          Gather#424 step 2/4 (+468ns): scatter:
                                                            Task#416: pool=0
                                                            Task#416 step 1/2 (+0s): 5.875µs self time
                                                            Task#416 step 2/2 (+5.875µs): return nil
                                                            Task#416 ends at 46.995µs
                                                              Gather#416: index=0
                                                              Gather#416 step 1/2 (+0s): 781ns self time
                                                              Gather#416 step 2/2 (+781ns): return nil
                                                              Gather#416 ends at 47.776µs
                                                          Gather#424 step 3/4 (+468ns): 533ns self time
                                                          Gather#424 step 4/4 (+1.001µs): return nil
                                                          Gather#424 ends at 41.653µs
                                                      Combine#428 step 3/4 (+159ns): 159ns self time
                                                      Combine#428 step 4/4 (+318ns): return nil
                                                      Combine#428 ends at 37.534µs
                                                  Gather#434 step 3/4 (+6.937µs): 6.941µs self time
                                                  Gather#434 step 4/4 (+13.878µs): return nil
                                                  Gather#434 ends at 34.157µs
                                              Gather#443 step 3/6 (+262ns): 392ns self time
                                              Gather#443 step 4/6 (+654ns): scatter:
                                                Task#405: pool=0
                                                Task#405 step 1/2 (+0s): 9.993µs self time
                                                Task#405 step 2/2 (+9.993µs): return nil
                                                Task#405 ends at 20.662µs
                                                  Combine#405: index=13 flush=<nil>
                                                  Combine#405 step 1/2 (+0s): 999ns self time
                                                  Combine#405 step 2/2 (+999ns): return nil
                                                  Combine#405 ends at 21.661µs
                                              Gather#443 step 5/6 (+654ns): 439ns self time
                                              Gather#443 step 6/6 (+1.093µs): return nil
                                              Gather#443 ends at 11.108µs
                                          Plan#18 step 4/8 (+0s): scatter:
                                            Task#412: pool=0
                                            Task#412 step 1/2 (+0s): 10µs self time
                                            Task#412 step 2/2 (+10µs): return nil
                                            Task#412 ends at 10µs
                                              Combine#412: index=11 flush=<nil>
                                              Combine#412 step 1/2 (+0s): 901ns self time
                                              Combine#412 step 2/2 (+901ns): return nil
                                              Combine#412 ends at 10.901µs
                                          Plan#18 step 5/8 (+0s): scatter:
                                            Task#414: pool=0
                                            Task#414 step 1/2 (+0s): 10.001µs self time
                                            Task#414 step 2/2 (+10.001µs): return nil
                                            Task#414 ends at 10.001µs
                                              Gather#414: index=0
                                              Gather#414 step 1/2 (+0s): 999ns self time
                                              Gather#414 step 2/2 (+999ns): return nil
                                              Gather#414 ends at 11µs
                                          Plan#18 step 6/8 (+0s): scatter:
                                            Task#406: pool=0
                                            Task#406 step 1/2 (+0s): 2.763116ms self time
                                            Task#406 step 2/2 (+2.763116ms): return nil
                                            Task#406 ends at 2.763116ms
                                              Combine#406: index=0 flush=<nil>
                                              Combine#406 step 1/2 (+0s): 653ns self time
                                              Combine#406 step 2/2 (+653ns): return nil
                                              Combine#406 ends at 2.763769ms
                                          Plan#18 step 7/8 (+0s): scatter:
                                            Task#440: pool=0
                                            Task#440 step 1/2 (+0s): 10.024µs self time
                                            Task#440 step 2/2 (+10.024µs): return nil
                                            Task#440 ends at 10.024µs
                                              Gather#440: index=0
                                              Gather#440 step 1/4 (+0s): 218ns self time
                                              Gather#440 step 2/4 (+218ns): scatter:
                                                Task#438: pool=0
                                                Task#438 step 1/2 (+0s): 2.291156ms self time
                                                Task#438 step 2/2 (+2.291156ms): return nil
                                                Task#438 ends at 2.301398ms
                                                  Gather#438: index=0
                                                  Gather#438 step 1/4 (+0s): 520ns self time
                                                  Gather#438 step 2/4 (+520ns): scatter:
                                                    Task#433: pool=0
                                                    Task#433 step 1/2 (+0s): 10.028µs self time
                                                    Task#433 step 2/2 (+10.028µs): return nil
                                                    Task#433 ends at 2.311946ms
                                                      Gather#433: index=0
                                                      Gather#433 step 1/6 (+0s): 323ns self time
                                                      Gather#433 step 2/6 (+323ns): scatter:
                                                        Task#421: pool=0
                                                        Task#421 step 1/2 (+0s): 0s self time
                                                        Task#421 step 2/2 (+0s): return nil
                                                        Task#421 ends at 2.312269ms
                                                          Gather#421: index=0
                                                          Gather#421 step 1/4 (+0s): 561ns self time
                                                          Gather#421 step 2/4 (+561ns): scatter:
                                                            Task#417: pool=0
                                                            Task#417 step 1/2 (+0s): 10µs self time
                                                            Task#417 step 2/2 (+10µs): return nil
                                                            Task#417 ends at 2.32283ms
                                                              Gather#417: index=0
                                                              Gather#417 step 1/2 (+0s): 1.002µs self time
                                                              Gather#417 step 2/2 (+1.002µs): return nil
                                                              Gather#417 ends at 2.323832ms
                                                          Gather#421 step 3/4 (+561ns): 440ns self time
                                                          Gather#421 step 4/4 (+1.001µs): return nil
                                                          Gather#421 ends at 2.31327ms
                                                      Gather#433 step 3/6 (+323ns): 350ns self time
                                                      Gather#433 step 4/6 (+673ns): scatter:
                                                        Task#423: pool=0
                                                        Task#423 step 1/2 (+0s): 6.294291ms self time
                                                        Task#423 step 2/2 (+6.294291ms): return nil
                                                        Task#423 ends at 8.60691ms
                                                          Gather#423: index=0
                                                          Gather#423 step 1/6 (+0s): 827ns self time
                                                          Gather#423 step 2/6 (+827ns): scatter:
                                                            Task#403: pool=0
                                                            Task#403 step 1/2 (+0s): 10.005µs self time
                                                            Task#403 step 2/2 (+10.005µs): return nil
                                                            Task#403 ends at 8.617742ms
                                                              Gather#403: index=0
                                                              Gather#403 step 1/2 (+0s): 755ns self time
                                                              Gather#403 step 2/2 (+755ns): return nil
                                                              Gather#403 ends at 8.618497ms
                                                          Gather#423 step 3/6 (+827ns): 125ns self time
                                                          Gather#423 step 4/6 (+952ns): scatter:
                                                            Task#415: pool=0
                                                            Task#415 step 1/2 (+0s): 10µs self time
                                                            Task#415 step 2/2 (+10µs): return nil
                                                            Task#415 ends at 8.617862ms
                                                              Gather#415: index=0
                                                              Gather#415 step 1/2 (+0s): 1.027µs self time
                                                              Gather#415 step 2/2 (+1.027µs): return nil
                                                              Gather#415 ends at 8.618889ms
                                                          Gather#423 step 5/6 (+952ns): 50ns self time
                                                          Gather#423 step 6/6 (+1.002µs): return nil
                                                          Gather#423 ends at 8.607912ms
                                                      Gather#433 step 5/6 (+673ns): 320ns self time
                                                      Gather#433 step 6/6 (+993ns): return error
                                                      Gather#433 ends at 2.312939ms
                                                  Gather#438 step 3/4 (+520ns): 215ns self time
                                                  Gather#438 step 4/4 (+735ns): return error
                                                  Gather#438 ends at 2.302133ms
                                              Gather#440 step 3/4 (+218ns): 47ns self time
                                              Gather#440 step 4/4 (+265ns): return nil
                                              Gather#440 ends at 10.289µs
                                          Plan#18 step 8/8 (+0s): ends at 8.618889ms
                                        Combine#402 step 3/4 (+8.619832ms): 0s self time
                                        Combine#402 step 4/4 (+8.619832ms): return nil
                                        Combine#402 ends at 8.640578ms
                                    Combine#541 step 3/14 (+728ns): 703ns self time
                                    Combine#541 step 4/14 (+1.431µs): scatter:
                                      Task#445: pool=2
                                      Task#445 step 1/2 (+0s): 1.445µs self time
                                      Task#445 step 2/2 (+1.445µs): return nil
                                      Task#445 ends at 12.874µs
                                        Combine#445: index=7 flush=<nil>
                                        Combine#445 step 1/2 (+0s): 985ns self time
                                        Combine#445 step 2/2 (+985ns): return nil
                                        Combine#445 ends at 13.859µs
                                    Combine#541 step 5/14 (+1.431µs): 722ns self time
                                    Combine#541 step 6/14 (+2.153µs): scatter:
                                      Task#455: pool=2
                                      Task#455 step 1/4 (+0s): 505ns self time
                                      Task#455 step 2/4 (+505ns): subjob:
                                        Plan#19: pathCount=29 taskCount=57 maxPathDuration=13.762195ms minGatherCount=48 maxGatherCount=57
                                           TaskPools[0]: TaskPool#57: limit=10
                                           CombinerPools[0]: CombinerPool#102: limit=1
                                           CombinerPools[1]: CombinerPool#103: limit=1
                                           CombinerPools[2]: CombinerPool#104: limit=4
                                           CombinerPools[3]: CombinerPool#105: limit=1
                                           CombinerPools[4]: CombinerPool#106: limit=1
                                           CombinerPools[5]: CombinerPool#107: limit=3
                                           CombinerPools[6]: CombinerPool#108: limit=10
                                           CombinerPools[7]: CombinerPool#109: limit=7
                                           CombinerPools[8]: CombinerPool#110: limit=2
                                           CombinerPools[9]: CombinerPool#111: limit=4
                                           Combiners[0]: pool=1
                                           Combiners[1]: pool=0
                                           Combiners[2]: pool=0
                                        Plan#19 step 1/13 (+0s): scatter:
                                          Task#509: pool=0
                                          Task#509 step 1/2 (+0s): 10.007µs self time
                                          Task#509 step 2/2 (+10.007µs): return nil
                                          Task#509 ends at 10.007µs
                                            Gather#509: index=0
                                            Gather#509 step 1/4 (+0s): 965ns self time
                                            Gather#509 step 2/4 (+965ns): scatter:
                                              Task#498: pool=0
                                              Task#498 step 1/2 (+0s): 9.985µs self time
                                              Task#498 step 2/2 (+9.985µs): return nil
                                              Task#498 ends at 20.957µs
                                                Gather#498: index=0
                                                Gather#498 step 1/4 (+0s): 509ns self time
                                                Gather#498 step 2/4 (+509ns): scatter:
                                                  Task#469: pool=0
                                                  Task#469 step 1/2 (+0s): 9.992µs self time
                                                  Task#469 step 2/2 (+9.992µs): return nil
                                                  Task#469 ends at 31.458µs
                                                    Gather#469: index=0
                                                    Gather#469 step 1/2 (+0s): 1.004µs self time
                                                    Gather#469 step 2/2 (+1.004µs): return error
                                                    Gather#469 ends at 32.462µs
                                                Gather#498 step 3/4 (+509ns): 516ns self time
                                                Gather#498 step 4/4 (+1.025µs): return nil
                                                Gather#498 ends at 21.982µs
                                            Gather#509 step 3/4 (+965ns): 37ns self time
                                            Gather#509 step 4/4 (+1.002µs): return nil
                                            Gather#509 ends at 11.009µs
                                        Plan#19 step 2/13 (+0s): scatter:
                                          Task#510: pool=0
                                          Task#510 step 1/2 (+0s): 9.99µs self time
                                          Task#510 step 2/2 (+9.99µs): return nil
                                          Task#510 ends at 9.99µs
                                            Gather#510: index=0
                                            Gather#510 step 1/4 (+0s): 499.791µs self time
                                            Gather#510 step 2/4 (+499.791µs): scatter:
                                              Task#500: pool=0
                                              Task#500 step 1/2 (+0s): 9.996µs self time
                                              Task#500 step 2/2 (+9.996µs): return nil
                                              Task#500 ends at 519.777µs
                                                Gather#500: index=0
                                                Gather#500 step 1/4 (+0s): 497ns self time
                                                Gather#500 step 2/4 (+497ns): scatter:
                                                  Task#480: pool=0
                                                  Task#480 step 1/2 (+0s): 9.999µs self time
                                                  Task#480 step 2/2 (+9.999µs): return nil
                                                  Task#480 ends at 530.273µs
                                                    Combine#480: index=2 flush=<nil>
                                                    Combine#480 step 1/2 (+0s): 1.179µs self time
                                                    Combine#480 step 2/2 (+1.179µs): return nil
                                                    Combine#480 ends at 531.452µs
                                                Gather#500 step 3/4 (+497ns): 495ns self time
                                                Gather#500 step 4/4 (+992ns): return nil
                                                Gather#500 ends at 520.769µs
                                            Gather#510 step 3/4 (+499.791µs): 500.209µs self time
                                            Gather#510 step 4/4 (+1ms): return nil
                                            Gather#510 ends at 1.00999ms
                                        Plan#19 step 3/13 (+0s): scatter:
                                          Task#508: pool=0
                                          Task#508 step 1/2 (+0s): 9.916µs self time
                                          Task#508 step 2/2 (+9.916µs): return nil
                                          Task#508 ends at 9.916µs
                                            Gather#508: index=0
                                            Gather#508 step 1/4 (+0s): 501ns self time
                                            Gather#508 step 2/4 (+501ns): scatter:
                                              Task#472: pool=0
                                              Task#472 step 1/2 (+0s): 9.285µs self time
                                              Task#472 step 2/2 (+9.285µs): return nil
                                              Task#472 ends at 19.702µs
                                                Gather#472: index=0
                                                Gather#472 step 1/2 (+0s): 382ns self time
                                                Gather#472 step 2/2 (+382ns): return nil
                                                Gather#472 ends at 20.084µs
                                            Gather#508 step 3/4 (+501ns): 499ns self time
                                            Gather#508 step 4/4 (+1µs): return nil
                                            Gather#508 ends at 10.916µs
                                        Plan#19 step 4/13 (+0s): scatter:
                                          Task#512: pool=0
                                          Task#512 step 1/2 (+0s): 10.1µs self time
                                          Task#512 step 2/2 (+10.1µs): return nil
                                          Task#512 ends at 10.1µs
                                            Gather#512: index=0
                                            Gather#512 step 1/4 (+0s): 280ns self time
                                            Gather#512 step 2/4 (+280ns): scatter:
                                              Task#474: pool=0
                                              Task#474 step 1/2 (+0s): 9.999µs self time
                                              Task#474 step 2/2 (+9.999µs): return nil
                                              Task#474 ends at 20.379µs
                                                Combine#474: index=1 flush=<nil>
                                                Combine#474 step 1/2 (+0s): 745ns self time
                                                Combine#474 step 2/2 (+745ns): return nil
                                                Combine#474 ends at 21.124µs
                                            Gather#512 step 3/4 (+280ns): 297ns self time
                                            Gather#512 step 4/4 (+577ns): return nil
                                            Gather#512 ends at 10.677µs
                                        Plan#19 step 5/13 (+0s): scatter:
                                          Task#456: pool=0
                                          Task#456 step 1/2 (+0s): 8.141µs self time
                                          Task#456 step 2/2 (+8.141µs): return nil
                                          Task#456 ends at 8.141µs
                                            Gather#456: index=0
                                            Gather#456 step 1/2 (+0s): 135.57µs self time
                                            Gather#456 step 2/2 (+135.57µs): return nil
                                            Gather#456 ends at 143.711µs
                                        Plan#19 step 6/13 (+0s): scatter:
                                          Task#507: pool=0
                                          Task#507 step 1/2 (+0s): 10.028µs self time
                                          Task#507 step 2/2 (+10.028µs): return nil
                                          Task#507 ends at 10.028µs
                                            Combine#507: index=2 flush=<nil>
                                            Combine#507 step 1/12 (+0s): 35ns self time
                                            Combine#507 step 2/12 (+35ns): scatter:
                                              Task#499: pool=0
                                              Task#499 step 1/2 (+0s): 9.998µs self time
                                              Task#499 step 2/2 (+9.998µs): return nil
                                              Task#499 ends at 20.061µs
                                                Gather#499: index=0
                                                Gather#499 step 1/4 (+0s): 582ns self time
                                                Gather#499 step 2/4 (+582ns): scatter:
                                                  Task#492: pool=0
                                                  Task#492 step 1/2 (+0s): 10µs self time
                                                  Task#492 step 2/2 (+10µs): return nil
                                                  Task#492 ends at 30.643µs
                                                    Gather#492: index=0
                                                    Gather#492 step 1/4 (+0s): 943ns self time
                                                    Gather#492 step 2/4 (+943ns): scatter:
                                                      Task#470: pool=0
                                                      Task#470 step 1/2 (+0s): 10.197µs self time
                                                      Task#470 step 2/2 (+10.197µs): return nil
                                                      Task#470 ends at 41.783µs
                                                        Gather#470: index=0
                                                        Gather#470 step 1/2 (+0s): 78.036µs self time
                                                        Gather#470 step 2/2 (+78.036µs): return nil
                                                        Gather#470 ends at 119.819µs
                                                    Gather#492 step 3/4 (+943ns): 149ns self time
                                                    Gather#492 step 4/4 (+1.092µs): return nil
                                                    Gather#492 ends at 31.735µs
                                                Gather#499 step 3/4 (+582ns): 404ns self time
                                                Gather#499 step 4/4 (+986ns): return error
                                                Gather#499 ends at 21.047µs
                                            Combine#507 step 3/12 (+35ns): 188ns self time
                                            Combine#507 step 4/12 (+223ns): scatter:
                                              Task#461: pool=0
                                              Task#461 step 1/2 (+0s): 8.338µs self time
                                              Task#461 step 2/2 (+8.338µs): return nil
                                              Task#461 ends at 18.589µs
                                                Gather#461: index=0
                                                Gather#461 step 1/2 (+0s): 1.001µs self time
                                                Gather#461 step 2/2 (+1.001µs): return nil
                                                Gather#461 ends at 19.59µs
                                            Combine#507 step 5/12 (+223ns): 637ns self time
                                            Combine#507 step 6/12 (+860ns): scatter:
                                              Task#475: pool=0
                                              Task#475 step 1/2 (+0s): 9.695µs self time
                                              Task#475 step 2/2 (+9.695µs): return nil
                                              Task#475 ends at 20.583µs
                                                Gather#475: index=0
                                                Gather#475 step 1/2 (+0s): 489.301µs self time
                                                Gather#475 step 2/2 (+489.301µs): return nil
                                                Gather#475 ends at 509.884µs
                                            Combine#507 step 7/12 (+860ns): 75ns self time
                                            Combine#507 step 8/12 (+935ns): scatter:
                                              Task#484: pool=0
                                              Task#484 step 1/2 (+0s): 10.002µs self time
                                              Task#484 step 2/2 (+10.002µs): return nil
                                              Task#484 ends at 20.965µs
                                                Gather#484: index=0
                                                Gather#484 step 1/2 (+0s): 1.007µs self time
                                                Gather#484 step 2/2 (+1.007µs): return nil
                                                Gather#484 ends at 21.972µs
                                            Combine#507 step 9/12 (+935ns): 27ns self time
                                            Combine#507 step 10/12 (+962ns): scatter:
                                              Task#503: pool=0
                                              Task#503 step 1/2 (+0s): 10.064µs self time
                                              Task#503 step 2/2 (+10.064µs): return nil
                                              Task#503 ends at 21.054µs
                                                Gather#503: index=0
                                                Gather#503 step 1/4 (+0s): 16.956µs self time
                                                Gather#503 step 2/4 (+16.956µs): scatter:
                                                  Task#495: pool=0
                                                  Task#495 step 1/2 (+0s): 9.784µs self time
                                                  Task#495 step 2/2 (+9.784µs): return nil
                                                  Task#495 ends at 47.794µs
                                                    Gather#495: index=0
                                                    Gather#495 step 1/4 (+0s): 962ns self time
                                                    Gather#495 step 2/4 (+962ns): scatter:
                                                      Task#477: pool=0
                                                      Task#477 step 1/2 (+0s): 10.122µs self time
                                                      Task#477 step 2/2 (+10.122µs): return nil
                                                      Task#477 ends at 58.878µs
                                                        Gather#477: index=0
                                                        Gather#477 step 1/2 (+0s): 1.001µs self time
                                                        Gather#477 step 2/2 (+1.001µs): return nil
                                                        Gather#477 ends at 59.879µs
                                                    Gather#495 step 3/4 (+962ns): 20ns self time
                                                    Gather#495 step 4/4 (+982ns): return error
                                                    Gather#495 ends at 48.776µs
                                                Gather#503 step 3/4 (+16.956µs): 16.907µs self time
                                                Gather#503 step 4/4 (+33.863µs): return nil
                                                Gather#503 ends at 54.917µs
                                            Combine#507 step 11/12 (+962ns): 36ns self time
                                            Combine#507 step 12/12 (+998ns): return nil
                                            Combine#507 ends at 11.026µs
                                        Plan#19 step 7/13 (+0s): scatter:
                                          Task#468: pool=0
                                          Task#468 step 1/2 (+0s): 9.841µs self time
                                          Task#468 step 2/2 (+9.841µs): return nil
                                          Task#468 ends at 9.841µs
                                            Gather#468: index=0
                                            Gather#468 step 1/2 (+0s): 1µs self time
                                            Gather#468 step 2/2 (+1µs): return nil
                                            Gather#468 ends at 10.841µs
                                        Plan#19 step 8/13 (+0s): scatter:
                                          Task#481: pool=0
                                          Task#481 step 1/2 (+0s): 9.983µs self time
                                          Task#481 step 2/2 (+9.983µs): return nil
                                          Task#481 ends at 9.983µs
                                            Gather#481: index=0
                                            Gather#481 step 1/2 (+0s): 997ns self time
                                            Gather#481 step 2/2 (+997ns): return nil
                                            Gather#481 ends at 10.98µs
                                        Plan#19 step 9/13 (+0s): scatter:
                                          Task#511: pool=0
                                          Task#511 step 1/2 (+0s): 0s self time
                                          Task#511 step 2/2 (+0s): return nil
                                          Task#511 ends at 0s
                                            Combine#511: index=0 flush=<nil>
                                            Combine#511 step 1/4 (+0s): 497ns self time
                                            Combine#511 step 2/4 (+497ns): scatter:
                                              Task#504: pool=0
                                              Task#504 step 1/2 (+0s): 9.998µs self time
                                              Task#504 step 2/2 (+9.998µs): return nil
                                              Task#504 ends at 10.495µs
                                                Combine#504: index=1 flush=<nil>
                                                Combine#504 step 1/4 (+0s): 593ns self time
                                                Combine#504 step 2/4 (+593ns): scatter:
                                                  Task#494: pool=0
                                                  Task#494 step 1/2 (+0s): 9.998µs self time
                                                  Task#494 step 2/2 (+9.998µs): return nil
                                                  Task#494 ends at 21.086µs
                                                    Combine#494: index=2 flush=<nil>
                                                    Combine#494 step 1/4 (+0s): 40ns self time
                                                    Combine#494 step 2/4 (+40ns): scatter:
                                                      Task#457: pool=0
                                                      Task#457 step 1/2 (+0s): 9.623µs self time
                                                      Task#457 step 2/2 (+9.623µs): return nil
                                                      Task#457 ends at 30.749µs
                                                        Combine#457: index=1 flush=<nil>
                                                        Combine#457 step 1/2 (+0s): 1ms self time
                                                        Combine#457 step 2/2 (+1ms): return nil
                                                        Combine#457 ends at 1.030749ms
                                                    Combine#494 step 3/4 (+40ns): 954ns self time
                                                    Combine#494 step 4/4 (+994ns): return nil
                                                    Combine#494 ends at 22.08µs
                                                Combine#504 step 3/4 (+593ns): 408ns self time
                                                Combine#504 step 4/4 (+1.001µs): return nil
                                                Combine#504 ends at 11.496µs
                                            Combine#511 step 3/4 (+497ns): 501ns self time
                                            Combine#511 step 4/4 (+998ns): return nil
                                            Combine#511 ends at 998ns
                                        Plan#19 step 10/13 (+0s): scatter:
                                          Task#505: pool=0
                                          Task#505 step 1/2 (+0s): 10.244µs self time
                                          Task#505 step 2/2 (+10.244µs): return nil
                                          Task#505 ends at 10.244µs
                                            Gather#505: index=0
                                            Gather#505 step 1/8 (+0s): 462ns self time
                                            Gather#505 step 2/8 (+462ns): scatter:
                                              Task#497: pool=0
                                              Task#497 step 1/2 (+0s): 586.958µs self time
                                              Task#497 step 2/2 (+586.958µs): return nil
                                              Task#497 ends at 597.664µs
                                                Gather#497: index=0
                                                Gather#497 step 1/12 (+0s): 37.814µs self time
                                                Gather#497 step 2/12 (+37.814µs): scatter:
                                                  Task#491: pool=0
                                                  Task#491 step 1/2 (+0s): 9.806µs self time
                                                  Task#491 step 2/2 (+9.806µs): return nil
                                                  Task#491 ends at 645.284µs
                                                    Gather#491: index=0
                                                    Gather#491 step 1/8 (+0s): 134ns self time
                                                    Gather#491 step 2/8 (+134ns): scatter:
                                                      Task#486: pool=0
                                                      Task#486 step 1/2 (+0s): 10.03µs self time
                                                      Task#486 step 2/2 (+10.03µs): return nil
                                                      Task#486 ends at 655.448µs
                                                        Gather#486: index=0
                                                        Gather#486 step 1/4 (+0s): 5.262µs self time
                                                        Gather#486 step 2/4 (+5.262µs): scatter:
                                                          Task#467: pool=0
                                                          Task#467 step 1/2 (+0s): 9.795µs self time
                                                          Task#467 step 2/2 (+9.795µs): return nil
                                                          Task#467 ends at 670.505µs
                                                            Gather#467: index=0
                                                            Gather#467 step 1/2 (+0s): 1.231µs self time
                                                            Gather#467 step 2/2 (+1.231µs): return nil
                                                            Gather#467 ends at 671.736µs
                                                        Gather#486 step 3/4 (+5.262µs): 5.264µs self time
                                                        Gather#486 step 4/4 (+10.526µs): return nil
                                                        Gather#486 ends at 665.974µs
                                                    Gather#491 step 3/8 (+134ns): 58ns self time
                                                    Gather#491 step 4/8 (+192ns): scatter:
                                                      Task#458: pool=0
                                                      Task#458 step 1/2 (+0s): 9.979µs self time
                                                      Task#458 step 2/2 (+9.979µs): return nil
                                                      Task#458 ends at 655.455µs
                                                        Gather#458: index=0
                                                        Gather#458 step 1/2 (+0s): 574ns self time
                                                        Gather#458 step 2/2 (+574ns): return nil
                                                        Gather#458 ends at 656.029µs
                                                    Gather#491 step 5/8 (+192ns): 99ns self time
                                                    Gather#491 step 6/8 (+291ns): scatter:
                                                      Task#465: pool=0
                                                      Task#465 step 1/2 (+0s): 10.326µs self time
                                                      Task#465 step 2/2 (+10.326µs): return nil
                                                      Task#465 ends at 655.901µs
                                                        Gather#465: index=0
                                                        Gather#465 step 1/2 (+0s): 991ns self time
                                                        Gather#465 step 2/2 (+991ns): return nil
                                                        Gather#465 ends at 656.892µs
                                                    Gather#491 step 7/8 (+291ns): 17ns self time
                                                    Gather#491 step 8/8 (+308ns): return nil
                                                    Gather#491 ends at 645.592µs
                                                Gather#497 step 3/12 (+37.814µs): 26.13µs self time
                                                Gather#497 step 4/12 (+63.944µs): scatter:
                                                  Task#471: pool=0
                                                  Task#471 step 1/2 (+0s): 50.344µs self time
                                                  Task#471 step 2/2 (+50.344µs): return nil
                                                  Task#471 ends at 711.952µs
                                                    Gather#471: index=0
                                                    Gather#471 step 1/2 (+0s): 913ns self time
                                                    Gather#471 step 2/2 (+913ns): return nil
                                                    Gather#471 ends at 712.865µs
                                                Gather#497 step 5/12 (+63.944µs): 40.722µs self time
                                                Gather#497 step 6/12 (+104.666µs): scatter:
                                                  Task#490: pool=0
                                                  Task#490 step 1/2 (+0s): 10.011µs self time
                                                  Task#490 step 2/2 (+10.011µs): return nil
                                                  Task#490 ends at 712.341µs
                                                    Gather#490: index=0
                                                    Gather#490 step 1/4 (+0s): 419ns self time
                                                    Gather#490 step 2/4 (+419ns): scatter:
                                                      Task#460: pool=0
                                                      Task#460 step 1/2 (+0s): 10.21µs self time
                                                      Task#460 step 2/2 (+10.21µs): return nil
                                                      Task#460 ends at 722.97µs
                                                        Gather#460: index=0
                                                        Gather#460 step 1/2 (+0s): 5.328µs self time
                                                        Gather#460 step 2/2 (+5.328µs): return nil
                                                        Gather#460 ends at 728.298µs
                                                    Gather#490 step 3/4 (+419ns): 440ns self time
                                                    Gather#490 step 4/4 (+859ns): return error
                                                    Gather#490 ends at 713.2µs
                                                Gather#497 step 7/12 (+104.666µs): 40.758µs self time
                                                Gather#497 step 8/12 (+145.424µs): scatter:
                                                  Task#479: pool=0
                                                  Task#479 step 1/2 (+0s): 1.563µs self time
                                                  Task#479 step 2/2 (+1.563µs): return nil
                                                  Task#479 ends at 744.651µs
                                                    Gather#479: index=0
                                                    Gather#479 step 1/2 (+0s): 994ns self time
                                                    Gather#479 step 2/2 (+994ns): return nil
                                                    Gather#479 ends at 745.645µs
                                                Gather#497 step 9/12 (+145.424µs): 42.058µs self time
                                                Gather#497 step 10/12 (+187.482µs): scatter:
                                                  Task#496: pool=0
                                                  Task#496 step 1/2 (+0s): 5.233µs self time
                                                  Task#496 step 2/2 (+5.233µs): return nil
                                                  Task#496 ends at 790.379µs
                                                    Gather#496: index=0
                                                    Gather#496 step 1/4 (+0s): 501ns self time
                                                    Gather#496 step 2/4 (+501ns): scatter:
                                                      Task#487: pool=0
                                                      Task#487 step 1/2 (+0s): 10.016µs self time
                                                      Task#487 step 2/2 (+10.016µs): return nil
                                                      Task#487 ends at 800.896µs
                                                        Gather#487: index=0
                                                        Gather#487 step 1/4 (+0s): 370ns self time
                                                        Gather#487 step 2/4 (+370ns): scatter:
                                                          Task#476: pool=0
                                                          Task#476 step 1/2 (+0s): 9.999µs self time
                                                          Task#476 step 2/2 (+9.999µs): return nil
                                                          Task#476 ends at 811.265µs
                                                            Gather#476: index=0
                                                            Gather#476 step 1/2 (+0s): 974ns self time
                                                            Gather#476 step 2/2 (+974ns): return nil
                                                            Gather#476 ends at 812.239µs
                                                        Gather#487 step 3/4 (+370ns): 617ns self time
                                                        Gather#487 step 4/4 (+987ns): return nil
                                                        Gather#487 ends at 801.883µs
                                                    Gather#496 step 3/4 (+501ns): 497ns self time
                                                    Gather#496 step 4/4 (+998ns): return error
                                                    Gather#496 ends at 791.377µs
                                                Gather#497 step 11/12 (+187.482µs): 39.419µs self time
                                                Gather#497 step 12/12 (+226.901µs): return error
                                                Gather#497 ends at 824.565µs
                                            Gather#505 step 3/8 (+462ns): 167ns self time
                                            Gather#505 step 4/8 (+629ns): scatter:
                                              Task#501: pool=0
                                              Task#501 step 1/2 (+0s): 11.248µs self time
                                              Task#501 step 2/2 (+11.248µs): return nil
                                              Task#501 ends at 22.121µs
                                                Gather#501: index=0
                                                Gather#501 step 1/4 (+0s): 507ns self time
                                                Gather#501 step 2/4 (+507ns): scatter:
                                                  Task#489: pool=0
                                                  Task#489 step 1/2 (+0s): 14.817µs self time
                                                  Task#489 step 2/2 (+14.817µs): return nil
                                                  Task#489 ends at 37.445µs
                                                    Gather#489: index=0
                                                    Gather#489 step 1/4 (+0s): 577ns self time
                                                    Gather#489 step 2/4 (+577ns): scatter:
                                                      Task#485: pool=0
                                                      Task#485 step 1/2 (+0s): 9.753µs self time
                                                      Task#485 step 2/2 (+9.753µs): return nil
                                                      Task#485 ends at 47.775µs
                                                        Gather#485: index=0
                                                        Gather#485 step 1/4 (+0s): 812ns self time
                                                        Gather#485 step 2/4 (+812ns): scatter:
                                                          Task#464: pool=0
                                                          Task#464 step 1/2 (+0s): 10.16µs self time
                                                          Task#464 step 2/2 (+10.16µs): return nil
                                                          Task#464 ends at 58.747µs
                                                            Gather#464: index=0
                                                            Gather#464 step 1/2 (+0s): 1.005µs self time
                                                            Gather#464 step 2/2 (+1.005µs): return nil
                                                            Gather#464 ends at 59.752µs
                                                        Gather#485 step 3/4 (+812ns): 195ns self time
                                                        Gather#485 step 4/4 (+1.007µs): return nil
                                                        Gather#485 ends at 48.782µs
                                                    Gather#489 step 3/4 (+577ns): 490ns self time
                                                    Gather#489 step 4/4 (+1.067µs): return nil
                                                    Gather#489 ends at 38.512µs
                                                Gather#501 step 3/4 (+507ns): 494ns self time
                                                Gather#501 step 4/4 (+1.001µs): return error
                                                Gather#501 ends at 23.122µs
                                            Gather#505 step 5/8 (+629ns): 133ns self time
                                            Gather#505 step 6/8 (+762ns): scatter:
                                              Task#459: pool=0
                                              Task#459 step 1/2 (+0s): 1.26651ms self time
                                              Task#459 step 2/2 (+1.26651ms): return nil
                                              Task#459 ends at 1.277516ms
                                                Gather#459: index=0
                                                Gather#459 step 1/2 (+0s): 973ns self time
                                                Gather#459 step 2/2 (+973ns): return nil
                                                Gather#459 ends at 1.278489ms
                                            Gather#505 step 7/8 (+762ns): 243ns self time
                                            Gather#505 step 8/8 (+1.005µs): return nil
                                            Gather#505 ends at 11.249µs
                                        Plan#19 step 11/13 (+0s): scatter:
                                          Task#506: pool=0
                                          Task#506 step 1/2 (+0s): 10ms self time
                                          Task#506 step 2/2 (+10ms): return nil
                                          Task#506 ends at 10ms
                                            Gather#506: index=0
                                            Gather#506 step 1/8 (+0s): 572ns self time
                                            Gather#506 step 2/8 (+572ns): scatter:
                                              Task#502: pool=0
                                              Task#502 step 1/2 (+0s): 9.985µs self time
                                              Task#502 step 2/2 (+9.985µs): return nil
                                              Task#502 ends at 10.010557ms
                                                Gather#502: index=0
                                                Gather#502 step 1/4 (+0s): 210ns self time
                                                Gather#502 step 2/4 (+210ns): scatter:
                                                  Task#493: pool=0
                                                  Task#493 step 1/2 (+0s): 21.066µs self time
                                                  Task#493 step 2/2 (+21.066µs): return nil
                                                  Task#493 ends at 10.031833ms
                                                    Gather#493: index=0
                                                    Gather#493 step 1/4 (+0s): 439ns self time
                                                    Gather#493 step 2/4 (+439ns): scatter:
                                                      Task#488: pool=0
                                                      Task#488 step 1/2 (+0s): 9.755µs self time
                                                      Task#488 step 2/2 (+9.755µs): return nil
                                                      Task#488 ends at 10.042027ms
                                                        Gather#488: index=0
                                                        Gather#488 step 1/10 (+0s): 938ns self time
                                                        Gather#488 step 2/10 (+938ns): scatter:
                                                          Task#473: pool=0
                                                          Task#473 step 1/2 (+0s): 3.718225ms self time
                                                          Task#473 step 2/2 (+3.718225ms): return nil
                                                          Task#473 ends at 13.76119ms
                                                            Gather#473: index=0
                                                            Gather#473 step 1/2 (+0s): 1.005µs self time
                                                            Gather#473 step 2/2 (+1.005µs): return nil
                                                            Gather#473 ends at 13.762195ms
                                                        Gather#488 step 3/10 (+938ns): 0s self time
                                                        Gather#488 step 4/10 (+938ns): scatter:
                                                          Task#478: pool=0
                                                          Task#478 step 1/2 (+0s): 10.001µs self time
                                                          Task#478 step 2/2 (+10.001µs): return nil
                                                          Task#478 ends at 10.052966ms
                                                            Gather#478: index=0
                                                            Gather#478 step 1/2 (+0s): 51.23µs self time
                                                            Gather#478 step 2/2 (+51.23µs): return nil
                                                            Gather#478 ends at 10.104196ms
                                                        Gather#488 step 5/10 (+938ns): 11ns self time
                                                        Gather#488 step 6/10 (+949ns): scatter:
                                                          Task#462: pool=0
                                                          Task#462 step 1/2 (+0s): 9.998µs self time
                                                          Task#462 step 2/2 (+9.998µs): return nil
                                                          Task#462 ends at 10.052974ms
                                                            Combine#462: index=0 flush=<nil>
                                                            Combine#462 step 1/2 (+0s): 117ns self time
                                                            Combine#462 step 2/2 (+117ns): return nil
                                                            Combine#462 ends at 10.053091ms
                                                        Gather#488 step 7/10 (+949ns): 17ns self time
                                                        Gather#488 step 8/10 (+966ns): scatter:
                                                          Task#466: pool=0
                                                          Task#466 step 1/2 (+0s): 5.1µs self time
                                                          Task#466 step 2/2 (+5.1µs): return nil
                                                          Task#466 ends at 10.048093ms
                                                            Gather#466: index=0
                                                            Gather#466 step 1/2 (+0s): 0s self time
                                                            Gather#466 step 2/2 (+0s): return nil
                                                            Gather#466 ends at 10.048093ms
                                                        Gather#488 step 9/10 (+966ns): 20ns self time
                                                        Gather#488 step 10/10 (+986ns): return nil
                                                        Gather#488 ends at 10.043013ms
                                                    Gather#493 step 3/4 (+439ns): 542ns self time
                                                    Gather#493 step 4/4 (+981ns): return nil
                                                    Gather#493 ends at 10.032814ms
                                                Gather#502 step 3/4 (+210ns): 355ns self time
                                                Gather#502 step 4/4 (+565ns): return nil
                                                Gather#502 ends at 10.011122ms
                                            Gather#506 step 3/8 (+572ns): 125ns self time
                                            Gather#506 step 4/8 (+697ns): scatter:
                                              Task#482: pool=0
                                              Task#482 step 1/2 (+0s): 10.171µs self time
                                              Task#482 step 2/2 (+10.171µs): return nil
                                              Task#482 ends at 10.010868ms
                                                Gather#482: index=0
                                                Gather#482 step 1/2 (+0s): 711ns self time
                                                Gather#482 step 2/2 (+711ns): return nil
                                                Gather#482 ends at 10.011579ms
                                            Gather#506 step 5/8 (+697ns): 113ns self time
                                            Gather#506 step 6/8 (+810ns): scatter:
                                              Task#463: pool=0
                                              Task#463 step 1/2 (+0s): 2.008µs self time
                                              Task#463 step 2/2 (+2.008µs): return nil
                                              Task#463 ends at 10.002818ms
                                                Combine#463: index=1 flush=<nil>
                                                Combine#463 step 1/2 (+0s): 997ns self time
                                                Combine#463 step 2/2 (+997ns): return nil
                                                Combine#463 ends at 10.003815ms
                                            Gather#506 step 7/8 (+810ns): 130ns self time
                                            Gather#506 step 8/8 (+940ns): return nil
                                            Gather#506 ends at 10.00094ms
                                        Plan#19 step 12/13 (+0s): scatter:
                                          Task#483: pool=0
                                          Task#483 step 1/2 (+0s): 10.109µs self time
                                          Task#483 step 2/2 (+10.109µs): return nil
                                          Task#483 ends at 10.109µs
                                            Gather#483: index=0
                                            Gather#483 step 1/2 (+0s): 851ns self time
                                            Gather#483 step 2/2 (+851ns): return nil
                                            Gather#483 ends at 10.96µs
                                        Plan#19 step 13/13 (+0s): ends at 13.762195ms
                                      Task#455 step 3/4 (+13.7627ms): 3.025µs self time
                                      Task#455 step 4/4 (+13.765725ms): return nil
                                      Task#455 ends at 13.777876ms
                                        Gather#455: index=18
                                        Gather#455 step 1/10 (+0s): 0s self time
                                        Gather#455 step 2/10 (+0s): scatter:
                                          Task#377: pool=1
                                          Task#377 step 1/2 (+0s): 9.939µs self time
                                          Task#377 step 2/2 (+9.939µs): return nil
                                          Task#377 ends at 13.787815ms
                                            Gather#377: index=8
                                            Gather#377 step 1/4 (+0s): 13ns self time
                                            Gather#377 step 2/4 (+13ns): subjob:
                                              Plan#17: pathCount=13 taskCount=24 maxPathDuration=4.787481ms minGatherCount=15 maxGatherCount=24
                                                 TaskPools[0]: TaskPool#54: limit=1
                                                 TaskPools[1]: TaskPool#55: limit=1
                                                 CombinerPools[0]: CombinerPool#86: limit=2
                                                 CombinerPools[1]: CombinerPool#87: limit=1
                                                 CombinerPools[2]: CombinerPool#88: limit=1
                                                 CombinerPools[3]: CombinerPool#89: limit=1
                                                 CombinerPools[4]: CombinerPool#90: limit=1
                                                 CombinerPools[5]: CombinerPool#91: limit=1
                                                 CombinerPools[6]: CombinerPool#92: limit=1
                                                 Combiners[0]: pool=6
                                                 Combiners[1]: pool=2
                                              Plan#17 step 1/5 (+0s): scatter:
                                                Task#401: pool=0
                                                Task#401 step 1/2 (+0s): 37.527µs self time
                                                Task#401 step 2/2 (+37.527µs): return nil
                                                Task#401 ends at 37.527µs
                                                  Combine#401: index=0 flush=<nil>
                                                  Combine#401 step 1/4 (+0s): 109ns self time
                                                  Combine#401 step 2/4 (+109ns): scatter:
                                                    Task#390: pool=0
                                                    Task#390 step 1/2 (+0s): 4.879µs self time
                                                    Task#390 step 2/2 (+4.879µs): return nil
                                                    Task#390 ends at 42.515µs
                                                      Gather#390: index=0
                                                      Gather#390 step 1/2 (+0s): 842ns self time
                                                      Gather#390 step 2/2 (+842ns): return nil
                                                      Gather#390 ends at 43.357µs
                                                  Combine#401 step 3/4 (+109ns): 112ns self time
                                                  Combine#401 step 4/4 (+221ns): return nil
                                                  Combine#401 ends at 37.748µs
                                              Plan#17 step 2/5 (+0s): scatter:
                                                Task#383: pool=0
                                                Task#383 step 1/2 (+0s): 9.999µs self time
                                                Task#383 step 2/2 (+9.999µs): return nil
                                                Task#383 ends at 9.999µs
                                                  Combine#383: index=0 flush=<nil>
                                                  Combine#383 step 1/2 (+0s): 999ns self time
                                                  Combine#383 step 2/2 (+999ns): return nil
                                                  Combine#383 ends at 10.998µs
                                              Plan#17 step 3/5 (+0s): scatter:
                                                Task#388: pool=1
                                                Task#388 step 1/2 (+0s): 9.994µs self time
                                                Task#388 step 2/2 (+9.994µs): return nil
                                                Task#388 ends at 9.994µs
                                                  Combine#388: index=1 flush=<nil>
                                                  Combine#388 step 1/2 (+0s): 975ns self time
                                                  Combine#388 step 2/2 (+975ns): return nil
                                                  Combine#388 ends at 10.969µs
                                              Plan#17 step 4/5 (+0s): scatter:
                                                Task#400: pool=1
                                                Task#400 step 1/2 (+0s): 10.259µs self time
                                                Task#400 step 2/2 (+10.259µs): return nil
                                                Task#400 ends at 10.259µs
                                                  Gather#400: index=0
                                                  Gather#400 step 1/16 (+0s): 168ns self time
                                                  Gather#400 step 2/16 (+168ns): scatter:
                                                    Task#396: pool=1
                                                    Task#396 step 1/2 (+0s): 10.104µs self time
                                                    Task#396 step 2/2 (+10.104µs): return nil
                                                    Task#396 ends at 20.531µs
                                                      Gather#396: index=0
                                                      Gather#396 step 1/4 (+0s): 447ns self time
                                                      Gather#396 step 2/4 (+447ns): scatter:
                                                        Task#382: pool=0
                                                        Task#382 step 1/2 (+0s): 9.999µs self time
                                                        Task#382 step 2/2 (+9.999µs): return nil
                                                        Task#382 ends at 30.977µs
                                                          Combine#382: index=0 flush=<nil>
                                                          Combine#382 step 1/2 (+0s): 534ns self time
                                                          Combine#382 step 2/2 (+534ns): return nil
                                                          Combine#382 ends at 31.511µs
                                                      Gather#396 step 3/4 (+447ns): 554ns self time
                                                      Gather#396 step 4/4 (+1.001µs): return nil
                                                      Gather#396 ends at 21.532µs
                                                  Gather#400 step 3/16 (+168ns): 137ns self time
                                                  Gather#400 step 4/16 (+305ns): scatter:
                                                    Task#395: pool=1
                                                    Task#395 step 1/2 (+0s): 9.999µs self time
                                                    Task#395 step 2/2 (+9.999µs): return nil
                                                    Task#395 ends at 20.563µs
                                                      Combine#395: index=0 flush=<nil>
                                                      Combine#395 step 1/4 (+0s): 284ns self time
                                                      Combine#395 step 2/4 (+284ns): scatter:
                                                        Task#386: pool=0
                                                        Task#386 step 1/2 (+0s): 9.916µs self time
                                                        Task#386 step 2/2 (+9.916µs): return nil
                                                        Task#386 ends at 30.763µs
                                                          Combine#386: index=0 flush=<nil>
                                                          Combine#386 step 1/2 (+0s): 998ns self time
                                                          Combine#386 step 2/2 (+998ns): return nil
                                                          Combine#386 ends at 31.761µs
                                                      Combine#395 step 3/4 (+284ns): 286ns self time
                                                      Combine#395 step 4/4 (+570ns): return nil
                                                      Combine#395 ends at 21.133µs
                                                  Gather#400 step 5/16 (+305ns): 92ns self time
                                                  Gather#400 step 6/16 (+397ns): scatter:
                                                    Task#379: pool=1
                                                    Task#379 step 1/2 (+0s): 9.877µs self time
                                                    Task#379 step 2/2 (+9.877µs): return nil
                                                    Task#379 ends at 20.533µs
                                                      Gather#379: index=0
                                                      Gather#379 step 1/2 (+0s): 887ns self time
                                                      Gather#379 step 2/2 (+887ns): return nil
                                                      Gather#379 ends at 21.42µs
                                                  Gather#400 step 7/16 (+397ns): 144ns self time
                                                  Gather#400 step 8/16 (+541ns): scatter:
                                                    Task#397: pool=0
                                                    Task#397 step 1/2 (+0s): 9.998µs self time
                                                    Task#397 step 2/2 (+9.998µs): return nil
                                                    Task#397 ends at 20.798µs
                                                      Gather#397: index=0
                                                      Gather#397 step 1/4 (+0s): 0s self time
                                                      Gather#397 step 2/4 (+0s): scatter:
                                                        Task#384: pool=0
                                                        Task#384 step 1/2 (+0s): 73.717µs self time
                                                        Task#384 step 2/2 (+73.717µs): return nil
                                                        Task#384 ends at 94.515µs
                                                          Gather#384: index=0
                                                          Gather#384 step 1/2 (+0s): 996ns self time
                                                          Gather#384 step 2/2 (+996ns): return nil
                                                          Gather#384 ends at 95.511µs
                                                      Gather#397 step 3/4 (+0s): 0s self time
                                                      Gather#397 step 4/4 (+0s): return nil
                                                      Gather#397 ends at 20.798µs
                                                  Gather#400 step 9/16 (+541ns): 143ns self time
                                                  Gather#400 step 10/16 (+684ns): scatter:
                                                    Task#398: pool=0
                                                    Task#398 step 1/2 (+0s): 9.963µs self time
                                                    Task#398 step 2/2 (+9.963µs): return nil
                                                    Task#398 ends at 20.906µs
                                                      Gather#398: index=0
                                                      Gather#398 step 1/4 (+0s): 1.309µs self time
                                                      Gather#398 step 2/4 (+1.309µs): scatter:
                                                        Task#393: pool=1
                                                        Task#393 step 1/2 (+0s): 0s self time
                                                        Task#393 step 2/2 (+0s): return nil
                                                        Task#393 ends at 22.215µs
                                                          Gather#393: index=0
                                                          Gather#393 step 1/4 (+0s): 333ns self time
                                                          Gather#393 step 2/4 (+333ns): scatter:
                                                            Task#391: pool=1
                                                            Task#391 step 1/2 (+0s): 9.998µs self time
                                                            Task#391 step 2/2 (+9.998µs): return nil
                                                            Task#391 ends at 32.546µs
                                                              Gather#391: index=0
                                                              Gather#391 step 1/8 (+0s): 892ns self time
                                                              Gather#391 step 2/8 (+892ns): scatter:
                                                                Task#389: pool=1
                                                                Task#389 step 1/2 (+0s): 7.219µs self time
                                                                Task#389 step 2/2 (+7.219µs): return nil
                                                                Task#389 ends at 40.657µs
                                                                  Gather#389: index=0
                                                                  Gather#389 step 1/2 (+0s): 996ns self time
                                                                  Gather#389 step 2/2 (+996ns): return nil
                                                                  Gather#389 ends at 41.653µs
                                                              Gather#391 step 3/8 (+892ns): 20ns self time
                                                              Gather#391 step 4/8 (+912ns): scatter:
                                                                Task#380: pool=0
                                                                Task#380 step 1/2 (+0s): 15.59µs self time
                                                                Task#380 step 2/2 (+15.59µs): return nil
                                                                Task#380 ends at 49.048µs
                                                                  Combine#380: index=0 flush=<nil>
                                                                  Combine#380 step 1/2 (+0s): 0s self time
                                                                  Combine#380 step 2/2 (+0s): return nil
                                                                  Combine#380 ends at 49.048µs
                                                              Gather#391 step 5/8 (+912ns): 66ns self time
                                                              Gather#391 step 6/8 (+978ns): scatter:
                                                                Task#387: pool=1
                                                                Task#387 step 1/2 (+0s): 10.37µs self time
                                                                Task#387 step 2/2 (+10.37µs): return nil
                                                                Task#387 ends at 43.894µs
                                                                  Gather#387: index=0
                                                                  Gather#387 step 1/2 (+0s): 825.821µs self time
                                                                  Gather#387 step 2/2 (+825.821µs): return nil
                                                                  Gather#387 ends at 869.715µs
                                                              Gather#391 step 7/8 (+978ns): 47ns self time
                                                              Gather#391 step 8/8 (+1.025µs): return nil
                                                              Gather#391 ends at 33.571µs
                                                          Gather#393 step 3/4 (+333ns): 672ns self time
                                                          Gather#393 step 4/4 (+1.005µs): return nil
                                                          Gather#393 ends at 23.22µs
                                                      Gather#398 step 3/4 (+1.309µs): 1.331µs self time
                                                      Gather#398 step 4/4 (+2.64µs): return nil
                                                      Gather#398 ends at 23.546µs
                                                  Gather#400 step 11/16 (+684ns): 82ns self time
                                                  Gather#400 step 12/16 (+766ns): scatter:
                                                    Task#399: pool=0
                                                    Task#399 step 1/2 (+0s): 9.975µs self time
                                                    Task#399 step 2/2 (+9.975µs): return nil
                                                    Task#399 ends at 21µs
                                                      Combine#399: index=0 flush=<nil>
                                                      Combine#399 step 1/4 (+0s): 768ns self time
                                                      Combine#399 step 2/4 (+768ns): scatter:
                                                        Task#394: pool=1
                                                        Task#394 step 1/2 (+0s): 9.997µs self time
                                                        Task#394 step 2/2 (+9.997µs): return error
                                                        Task#394 ends at 31.765µs
                                                          Gather#394: index=0
                                                          Gather#394 step 1/4 (+0s): 499ns self time
                                                          Gather#394 step 2/4 (+499ns): scatter:
                                                            Task#392: pool=0
                                                            Task#392 step 1/2 (+0s): 9.961µs self time
                                                            Task#392 step 2/2 (+9.961µs): return nil
                                                            Task#392 ends at 42.225µs
                                                              Gather#392: index=0
                                                              Gather#392 step 1/6 (+0s): 20.888µs self time
                                                              Gather#392 step 2/6 (+20.888µs): scatter:
                                                                Task#378: pool=1
                                                                Task#378 step 1/2 (+0s): 10µs self time
                                                                Task#378 step 2/2 (+10µs): return nil
                                                                Task#378 ends at 73.113µs
                                                                  Gather#378: index=0
                                                                  Gather#378 step 1/2 (+0s): 203.92µs self time
                                                                  Gather#378 step 2/2 (+203.92µs): return nil
                                                                  Gather#378 ends at 277.033µs
                                                              Gather#392 step 3/6 (+20.888µs): 20.91µs self time
                                                              Gather#392 step 4/6 (+41.798µs): scatter:
                                                                Task#385: pool=1
                                                                Task#385 step 1/2 (+0s): 23.247µs self time
                                                                Task#385 step 2/2 (+23.247µs): return nil
                                                                Task#385 ends at 107.27µs
                                                                  Combine#385: index=1 flush=<nil>
                                                                  Combine#385 step 1/2 (+0s): 1.003µs self time
                                                                  Combine#385 step 2/2 (+1.003µs): return nil
                                                                  Combine#385 ends at 108.273µs
                                                              Gather#392 step 5/6 (+41.798µs): 20.872µs self time
                                                              Gather#392 step 6/6 (+62.67µs): return nil
                                                              Gather#392 ends at 104.895µs
                                                          Gather#394 step 3/4 (+499ns): 504ns self time
                                                          Gather#394 step 4/4 (+1.003µs): return nil
                                                          Gather#394 ends at 32.768µs
                                                      Combine#399 step 3/4 (+768ns): 770ns self time
                                                      Combine#399 step 4/4 (+1.538µs): return nil
                                                      Combine#399 ends at 22.538µs
                                                  Gather#400 step 13/16 (+766ns): 223ns self time
                                                  Gather#400 step 14/16 (+989ns): scatter:
                                                    Task#381: pool=0
                                                    Task#381 step 1/2 (+0s): 4.775231ms self time
                                                    Task#381 step 2/2 (+4.775231ms): return nil
                                                    Task#381 ends at 4.786479ms
                                                      Gather#381: index=0
                                                      Gather#381 step 1/2 (+0s): 1.002µs self time
                                                      Gather#381 step 2/2 (+1.002µs): return nil
                                                      Gather#381 ends at 4.787481ms
                                                  Gather#400 step 15/16 (+989ns): 133ns self time
                                                  Gather#400 step 16/16 (+1.122µs): return nil
                                                  Gather#400 ends at 11.381µs
                                              Plan#17 step 5/5 (+0s): ends at 4.787481ms
                                            Gather#377 step 3/4 (+4.787494ms): 55ns self time
                                            Gather#377 step 4/4 (+4.787549ms): return nil
                                            Gather#377 ends at 18.575364ms
                                        Gather#455 step 3/10 (+0s): 0s self time
                                        Gather#455 step 4/10 (+0s): scatter:
                                          Task#453: pool=2
                                          Task#453 step 1/2 (+0s): 10.117µs self time
                                          Task#453 step 2/2 (+10.117µs): return nil
                                          Task#453 ends at 13.787993ms
                                            Combine#453: index=1 flush=<nil>
                                            Combine#453 step 1/6 (+0s): 37.544µs self time
                                            Combine#453 step 2/6 (+37.544µs): scatter:
                                              Task#451: pool=1
                                              Task#451 step 1/2 (+0s): 147.645µs self time
                                              Task#451 step 2/2 (+147.645µs): return nil
                                              Task#451 ends at 13.973182ms
                                                Gather#451: index=3
                                                Gather#451 step 1/4 (+0s): 502ns self time
                                                Gather#451 step 2/4 (+502ns): scatter:
                                                  Task#288: pool=1
                                                  Task#288 step 1/2 (+0s): 10.017µs self time
                                                  Task#288 step 2/2 (+10.017µs): return nil
                                                  Task#288 ends at 13.983701ms
                                                    Combine#288: index=2 flush=<nil>
                                                    Combine#288 step 1/2 (+0s): 996ns self time
                                                    Combine#288 step 2/2 (+996ns): return nil
                                                    Combine#288 ends at 13.984697ms
                                                Gather#451 step 3/4 (+502ns): 503ns self time
                                                Gather#451 step 4/4 (+1.005µs): return nil
                                                Gather#451 ends at 13.974187ms
                                            Combine#453 step 3/6 (+37.544µs): 37.541µs self time
                                            Combine#453 step 4/6 (+75.085µs): scatter:
                                              Task#448: pool=2
                                              Task#448 step 1/2 (+0s): 10.491µs self time
                                              Task#448 step 2/2 (+10.491µs): return nil
                                              Task#448 ends at 13.873569ms
                                                Gather#448: index=12
                                                Gather#448 step 1/2 (+0s): 1µs self time
                                                Gather#448 step 2/2 (+1µs): return nil
                                                Gather#448 ends at 13.874569ms
                                            Combine#453 step 5/6 (+75.085µs): 37.538µs self time
                                            Combine#453 step 6/6 (+112.623µs): return nil
                                            Combine#453 ends at 13.900616ms
                                        Gather#455 step 5/10 (+0s): 0s self time
                                        Gather#455 step 6/10 (+0s): scatter:
                                          Task#452: pool=1
                                          Task#452 step 1/2 (+0s): 10.019µs self time
                                          Task#452 step 2/2 (+10.019µs): return nil
                                          Task#452 ends at 13.787895ms
                                            Combine#452: index=8 flush=Gather#452
                                            Combine#452 step 1/2 (+0s): 2.014µs self time
                                            Combine#452 step 2/2 (+2.014µs): return nil
                                            Combine#452 ends at 13.789909ms
                                              Gather#452: index=5
                                              Gather#452 step 1/4 (+0s): 562ns self time
                                              Gather#452 step 2/4 (+562ns): scatter:
                                                Task#327: pool=2
                                                Task#327 step 1/2 (+0s): 10.062µs self time
                                                Task#327 step 2/2 (+10.062µs): return nil
                                                Task#327 ends at 0s
                                                  Gather#327: index=3
                                                  Gather#327 step 1/2 (+0s): 988ns self time
                                                  Gather#327 step 2/2 (+988ns): return nil
                                                  Gather#327 ends at 0s
                                              Gather#452 step 3/4 (+562ns): 553ns self time
                                              Gather#452 step 4/4 (+1.115µs): return nil
                                              Gather#452 ends at 0s
                                        Gather#455 step 7/10 (+0s): 0s self time
                                        Gather#455 step 8/10 (+0s): scatter:
                                          Task#290: pool=2
                                          Task#290 step 1/2 (+0s): 10.061µs self time
                                          Task#290 step 2/2 (+10.061µs): return nil
                                          Task#290 ends at 13.787937ms
                                            Gather#290: index=1
                                            Gather#290 step 1/2 (+0s): 97.052µs self time
                                            Gather#290 step 2/2 (+97.052µs): return nil
                                            Gather#290 ends at 13.884989ms
                                        Gather#455 step 9/10 (+0s): 0s self time
                                        Gather#455 step 10/10 (+0s): return nil
                                        Gather#455 ends at 13.777876ms
                                    Combine#541 step 7/14 (+2.153µs): 389ns self time
                                    Combine#541 step 8/14 (+2.542µs): scatter:
                                      Task#447: pool=1
                                      Task#447 step 1/2 (+0s): 10µs self time
                                      Task#447 step 2/2 (+10µs): return nil
                                      Task#447 ends at 22.54µs
                                        Combine#447: index=10 flush=<nil>
                                        Combine#447 step 1/2 (+0s): 1.001µs self time
                                        Combine#447 step 2/2 (+1.001µs): return nil
                                        Combine#447 ends at 23.541µs
                                    Combine#541 step 9/14 (+2.542µs): 757ns self time
                                    Combine#541 step 10/14 (+3.299µs): scatter:
                                      Task#454: pool=1
                                      Task#454 step 1/2 (+0s): 10ms self time
                                      Task#454 step 2/2 (+10ms): return nil
                                      Task#454 ends at 10.013297ms
                                        Gather#454: index=8
                                        Gather#454 step 1/10 (+0s): 490ns self time
                                        Gather#454 step 2/10 (+490ns): scatter:
                                          Task#444: pool=0
                                          Task#444 step 1/2 (+0s): 10µs self time
                                          Task#444 step 2/2 (+10µs): return error
                                          Task#444 ends at 10.023787ms
                                            Gather#444: index=8
                                            Gather#444 step 1/2 (+0s): 584ns self time
                                            Gather#444 step 2/2 (+584ns): return nil
                                            Gather#444 ends at 10.024371ms
                                        Gather#454 step 3/10 (+490ns): 123ns self time
                                        Gather#454 step 4/10 (+613ns): scatter:
                                          Task#295: pool=1
                                          Task#295 step 1/2 (+0s): 0s self time
                                          Task#295 step 2/2 (+0s): return nil
                                          Task#295 ends at 10.01391ms
                                            Gather#295: index=3
                                            Gather#295 step 1/2 (+0s): 1ms self time
                                            Gather#295 step 2/2 (+1ms): return nil
                                            Gather#295 ends at 11.01391ms
                                        Gather#454 step 5/10 (+613ns): 10ns self time
                                        Gather#454 step 6/10 (+623ns): scatter:
                                          Task#297: pool=0
                                          Task#297 step 1/2 (+0s): 9.864µs self time
                                          Task#297 step 2/2 (+9.864µs): return nil
                                          Task#297 ends at 10.023784ms
                                            Gather#297: index=4
                                            Gather#297 step 1/2 (+0s): 1.122µs self time
                                            Gather#297 step 2/2 (+1.122µs): return nil
                                            Gather#297 ends at 10.024906ms
                                        Gather#454 step 7/10 (+623ns): 161ns self time
                                        Gather#454 step 8/10 (+784ns): scatter:
                                          Task#298: pool=0
                                          Task#298 step 1/2 (+0s): 9.985µs self time
                                          Task#298 step 2/2 (+9.985µs): return nil
                                          Task#298 ends at 10.024066ms
                                            Combine#298: index=0 flush=Gather#298
                                            Combine#298 step 1/2 (+0s): 1.025µs self time
                                            Combine#298 step 2/2 (+1.025µs): return nil
                                            Combine#298 ends at 10.025091ms
                                              Gather#298: index=1
                                              Gather#298 step 1/2 (+0s): 1.001µs self time
                                              Gather#298 step 2/2 (+1.001µs): return nil
                                              Gather#298 ends at 0s
                                        Gather#454 step 9/10 (+784ns): 215ns self time
                                        Gather#454 step 10/10 (+999ns): return nil
                                        Gather#454 ends at 10.014296ms
                                    Combine#541 step 11/14 (+3.299µs): 867ns self time
                                    Combine#541 step 12/14 (+4.166µs): scatter:
                                      Task#294: pool=0
                                      Task#294 step 1/2 (+0s): 10.345µs self time
                                      Task#294 step 2/2 (+10.345µs): return nil
                                      Task#294 ends at 24.509µs
                                        Gather#294: index=6
                                        Gather#294 step 1/2 (+0s): 356ns self time
                                        Gather#294 step 2/2 (+356ns): return nil
                                        Gather#294 ends at 24.865µs
                                    Combine#541 step 13/14 (+4.166µs): 874ns self time
                                    Combine#541 step 14/14 (+5.04µs): return nil
                                    Combine#541 ends at 15.038µs
                                Plan#13 step 3/12 (+0s): scatter:
                                  Task#299: pool=1
                                  Task#299 step 1/2 (+0s): 9.999µs self time
                                  Task#299 step 2/2 (+9.999µs): return nil
                                  Task#299 ends at 9.999µs
                                    Gather#299: index=13
                                    Gather#299 step 1/4 (+0s): 521ns self time
                                    Gather#299 step 2/4 (+521ns): subjob:
                                      Plan#14: pathCount=20 taskCount=26 maxPathDuration=6.169932ms minGatherCount=21 maxGatherCount=35
                                         TaskPools[0]: TaskPool#43: limit=4
                                         TaskPools[1]: TaskPool#44: limit=1
                                         CombinerPools[0]: CombinerPool#67: limit=2
                                         CombinerPools[1]: CombinerPool#68: limit=2
                                         CombinerPools[2]: CombinerPool#69: limit=1
                                         CombinerPools[3]: CombinerPool#70: limit=4
                                         CombinerPools[4]: CombinerPool#71: limit=7
                                         CombinerPools[5]: CombinerPool#72: limit=2
                                         CombinerPools[6]: CombinerPool#73: limit=1
                                         CombinerPools[7]: CombinerPool#74: limit=6
                                         CombinerPools[8]: CombinerPool#75: limit=3
                                         Combiners[0]: pool=2
                                         Combiners[1]: pool=7
                                         Combiners[2]: pool=3
                                         Combiners[3]: pool=7
                                         Combiners[4]: pool=0
                                         Combiners[5]: pool=6
                                         Combiners[6]: pool=2
                                         Combiners[7]: pool=1
                                      Plan#14 step 1/6 (+0s): scatter:
                                        Task#318: pool=1
                                        Task#318 step 1/2 (+0s): 6.169225ms self time
                                        Task#318 step 2/2 (+6.169225ms): return nil
                                        Task#318 ends at 6.169225ms
                                          Gather#318: index=0
                                          Gather#318 step 1/2 (+0s): 707ns self time
                                          Gather#318 step 2/2 (+707ns): return nil
                                          Gather#318 ends at 6.169932ms
                                      Plan#14 step 2/6 (+0s): scatter:
                                        Task#303: pool=0
                                        Task#303 step 1/2 (+0s): 12.988µs self time
                                        Task#303 step 2/2 (+12.988µs): return nil
                                        Task#303 ends at 12.988µs
                                          Gather#303: index=8
                                          Gather#303 step 1/2 (+0s): 1.283µs self time
                                          Gather#303 step 2/2 (+1.283µs): return nil
                                          Gather#303 ends at 14.271µs
                                      Plan#14 step 3/6 (+0s): scatter:
                                        Task#325: pool=0
                                        Task#325 step 1/2 (+0s): 10µs self time
                                        Task#325 step 2/2 (+10µs): return nil
                                        Task#325 ends at 10µs
                                          Gather#325: index=8
                                          Gather#325 step 1/4 (+0s): 851ns self time
                                          Gather#325 step 2/4 (+851ns): scatter:
                                            Task#319: pool=1
                                            Task#319 step 1/2 (+0s): 10.001µs self time
                                            Task#319 step 2/2 (+10.001µs): return nil
                                            Task#319 ends at 20.852µs
                                              Gather#319: index=8
                                              Gather#319 step 1/2 (+0s): 1.005µs self time
                                              Gather#319 step 2/2 (+1.005µs): return nil
                                              Gather#319 ends at 21.857µs
                                          Gather#325 step 3/4 (+851ns): 131ns self time
                                          Gather#325 step 4/4 (+982ns): return nil
                                          Gather#325 ends at 10.982µs
                                      Plan#14 step 4/6 (+0s): scatter:
                                        Task#324: pool=0
                                        Task#324 step 1/2 (+0s): 9.972µs self time
                                        Task#324 step 2/2 (+9.972µs): return nil
                                        Task#324 ends at 9.972µs
                                          Gather#324: index=6
                                          Gather#324 step 1/14 (+0s): 122ns self time
                                          Gather#324 step 2/14 (+122ns): scatter:
                                            Task#304: pool=1
                                            Task#304 step 1/2 (+0s): 1.572µs self time
                                            Task#304 step 2/2 (+1.572µs): return nil
                                            Task#304 ends at 11.666µs
                                              Gather#304: index=5
                                              Gather#304 step 1/2 (+0s): 997ns self time
                                              Gather#304 step 2/2 (+997ns): return nil
                                              Gather#304 ends at 12.663µs
                                          Gather#324 step 3/14 (+122ns): 883ns self time
                                          Gather#324 step 4/14 (+1.005µs): scatter:
                                            Task#308: pool=1
                                            Task#308 step 1/2 (+0s): 11.585µs self time
                                            Task#308 step 2/2 (+11.585µs): return nil
                                            Task#308 ends at 22.562µs
                                              Combine#308: index=7 flush=<nil>
                                              Combine#308 step 1/2 (+0s): 1.001µs self time
                                              Combine#308 step 2/2 (+1.001µs): return nil
                                              Combine#308 ends at 23.563µs
                                          Gather#324 step 5/14 (+1.005µs): 0s self time
                                          Gather#324 step 6/14 (+1.005µs): scatter:
                                            Task#322: pool=1
                                            Task#322 step 1/2 (+0s): 10.007µs self time
                                            Task#322 step 2/2 (+10.007µs): return nil
                                            Task#322 ends at 20.984µs
                                              Gather#322: index=8
                                              Gather#322 step 1/16 (+0s): 311ns self time
                                              Gather#322 step 2/16 (+311ns): scatter:
                                                Task#306: pool=0
                                                Task#306 step 1/2 (+0s): 10.002µs self time
                                                Task#306 step 2/2 (+10.002µs): return nil
                                                Task#306 ends at 31.297µs
                                                  Gather#306: index=1
                                                  Gather#306 step 1/2 (+0s): 1.001µs self time
                                                  Gather#306 step 2/2 (+1.001µs): return nil
                                                  Gather#306 ends at 32.298µs
                                              Gather#322 step 3/16 (+311ns): 600ns self time
                                              Gather#322 step 4/16 (+911ns): scatter:
                                                Task#305: pool=0
                                                Task#305 step 1/2 (+0s): 10.009µs self time
                                                Task#305 step 2/2 (+10.009µs): return nil
                                                Task#305 ends at 31.904µs
                                                  Gather#305: index=5
                                                  Gather#305 step 1/2 (+0s): 1.184µs self time
                                                  Gather#305 step 2/2 (+1.184µs): return nil
                                                  Gather#305 ends at 33.088µs
                                              Gather#322 step 5/16 (+911ns): 15ns self time
                                              Gather#322 step 6/16 (+926ns): scatter:
                                                Task#307: pool=0
                                                Task#307 step 1/2 (+0s): 9.998µs self time
                                                Task#307 step 2/2 (+9.998µs): return nil
                                                Task#307 ends at 31.908µs
                                                  Combine#307: index=0 flush=<nil>
                                                  Combine#307 step 1/2 (+0s): 951ns self time
                                                  Combine#307 step 2/2 (+951ns): return nil
                                                  Combine#307 ends at 32.859µs
                                              Gather#322 step 7/16 (+926ns): 43ns self time
                                              Gather#322 step 8/16 (+969ns): scatter:
                                                Task#315: pool=0
                                                Task#315 step 1/2 (+0s): 9.911µs self time
                                                Task#315 step 2/2 (+9.911µs): return nil
                                                Task#315 ends at 31.864µs
                                                  Gather#315: index=4
                                                  Gather#315 step 1/2 (+0s): 968ns self time
                                                  Gather#315 step 2/2 (+968ns): return nil
                                                  Gather#315 ends at 32.832µs
                                              Gather#322 step 9/16 (+969ns): 12ns self time
                                              Gather#322 step 10/16 (+981ns): scatter:
                                                Task#309: pool=0
                                                Task#309 step 1/2 (+0s): 5.564µs self time
                                                Task#309 step 2/2 (+5.564µs): return nil
                                                Task#309 ends at 27.529µs
                                                  Gather#309: index=8
                                                  Gather#309 step 1/2 (+0s): 998ns self time
                                                  Gather#309 step 2/2 (+998ns): return nil
                                                  Gather#309 ends at 28.527µs
                                              Gather#322 step 11/16 (+981ns): 4ns self time
                                              Gather#322 step 12/16 (+985ns): scatter:
                                                Task#312: pool=1
                                                Task#312 step 1/2 (+0s): 9.988µs self time
                                                Task#312 step 2/2 (+9.988µs): return nil
                                                Task#312 ends at 31.957µs
                                                  Combine#312: index=1 flush=<nil>
                                                  Combine#312 step 1/2 (+0s): 2.944µs self time
                                                  Combine#312 step 2/2 (+2.944µs): return nil
                                                  Combine#312 ends at 34.901µs
                                              Gather#322 step 13/16 (+985ns): 7ns self time
                                              Gather#322 step 14/16 (+992ns): scatter:
                                                Task#317: pool=0
                                                Task#317 step 1/2 (+0s): 10.001µs self time
                                                Task#317 step 2/2 (+10.001µs): return error
                                                Task#317 ends at 31.977µs
                                                  Gather#317: index=5
                                                  Gather#317 step 1/2 (+0s): 1.003µs self time
                                                  Gather#317 step 2/2 (+1.003µs): return nil
                                                  Gather#317 ends at 32.98µs
                                              Gather#322 step 15/16 (+992ns): 4ns self time
                                              Gather#322 step 16/16 (+996ns): return nil
                                              Gather#322 ends at 21.98µs
                                          Gather#324 step 7/14 (+1.005µs): 0s self time
                                          Gather#324 step 8/14 (+1.005µs): scatter:
                                            Task#310: pool=1
                                            Task#310 step 1/2 (+0s): 9.802µs self time
                                            Task#310 step 2/2 (+9.802µs): return nil
                                            Task#310 ends at 20.779µs
                                              Gather#310: index=1
                                              Gather#310 step 1/2 (+0s): 668ns self time
                                              Gather#310 step 2/2 (+668ns): return nil
                                              Gather#310 ends at 21.447µs
                                          Gather#324 step 9/14 (+1.005µs): 0s self time
                                          Gather#324 step 10/14 (+1.005µs): scatter:
                                            Task#301: pool=1
                                            Task#301 step 1/2 (+0s): 10.013µs self time
                                            Task#301 step 2/2 (+10.013µs): return nil
                                            Task#301 ends at 20.99µs
                                              Gather#301: index=3
                                              Gather#301 step 1/2 (+0s): 99ns self time
                                              Gather#301 step 2/2 (+99ns): return nil
                                              Gather#301 ends at 21.089µs
                                          Gather#324 step 11/14 (+1.005µs): 0s self time
                                          Gather#324 step 12/14 (+1.005µs): scatter:
                                            Task#323: pool=1
                                            Task#323 step 1/2 (+0s): 0s self time
                                            Task#323 step 2/2 (+0s): return nil
                                            Task#323 ends at 10.977µs
                                              Gather#323: index=3
                                              Gather#323 step 1/8 (+0s): 10.144µs self time
                                              Gather#323 step 2/8 (+10.144µs): scatter:
                                                Task#300: pool=1
                                                Task#300 step 1/2 (+0s): 9.998µs self time
                                                Task#300 step 2/2 (+9.998µs): return nil
                                                Task#300 ends at 31.119µs
                                                  Gather#300: index=7
                                                  Gather#300 step 1/2 (+0s): 998ns self time
                                                  Gather#300 step 2/2 (+998ns): return error
                                                  Gather#300 ends at 32.117µs
                                              Gather#323 step 3/8 (+10.144µs): 10.139µs self time
                                              Gather#323 step 4/8 (+20.283µs): scatter:
                                                Task#321: pool=1
                                                Task#321 step 1/2 (+0s): 9.854µs self time
                                                Task#321 step 2/2 (+9.854µs): return nil
                                                Task#321 ends at 41.114µs
                                                  Gather#321: index=3
                                                  Gather#321 step 1/8 (+0s): 3.658µs self time
                                                  Gather#321 step 2/8 (+3.658µs): scatter:
                                                    Task#314: pool=1
                                                    Task#314 step 1/2 (+0s): 10.002µs self time
                                                    Task#314 step 2/2 (+10.002µs): return nil
                                                    Task#314 ends at 54.774µs
                                                      Gather#314: index=5
                                                      Gather#314 step 1/2 (+0s): 756.484µs self time
                                                      Gather#314 step 2/2 (+756.484µs): return nil
                                                      Gather#314 ends at 811.258µs
                                                  Gather#321 step 3/8 (+3.658µs): 3.639µs self time
                                                  Gather#321 step 4/8 (+7.297µs): scatter:
                                                    Task#320: pool=1
                                                    Task#320 step 1/2 (+0s): 9.968µs self time
                                                    Task#320 step 2/2 (+9.968µs): return nil
                                                    Task#320 ends at 58.379µs
                                                      Combine#320: index=2 flush=<nil>
                                                      Combine#320 step 1/4 (+0s): 744ns self time
                                                      Combine#320 step 2/4 (+744ns): scatter:
                                                        Task#302: pool=0
                                                        Task#302 step 1/2 (+0s): 10µs self time
                                                        Task#302 step 2/2 (+10µs): return nil
                                                        Task#302 ends at 69.123µs
                                                          Combine#302: index=0 flush=<nil>
                                                          Combine#302 step 1/2 (+0s): 974ns self time
                                                          Combine#302 step 2/2 (+974ns): return nil
                                                          Combine#302 ends at 70.097µs
                                                      Combine#320 step 3/4 (+744ns): 748ns self time
                                                      Combine#320 step 4/4 (+1.492µs): return nil
                                                      Combine#320 ends at 59.871µs
                                                  Gather#321 step 5/8 (+7.297µs): 5.394µs self time
                                                  Gather#321 step 6/8 (+12.691µs): scatter:
                                                    Task#311: pool=0
                                                    Task#311 step 1/2 (+0s): 10.002µs self time
                                                    Task#311 step 2/2 (+10.002µs): return nil
                                                    Task#311 ends at 63.807µs
                                                      Gather#311: index=3
                                                      Gather#311 step 1/2 (+0s): 1.12µs self time
                                                      Gather#311 step 2/2 (+1.12µs): return nil
                                                      Gather#311 ends at 64.927µs
                                                  Gather#321 step 7/8 (+12.691µs): 1.896µs self time
                                                  Gather#321 step 8/8 (+14.587µs): return nil
                                                  Gather#321 ends at 55.701µs
                                              Gather#323 step 5/8 (+20.283µs): 10.166µs self time
                                              Gather#323 step 6/8 (+30.449µs): scatter:
                                                Task#316: pool=1
                                                Task#316 step 1/2 (+0s): 10.001µs self time
                                                Task#316 step 2/2 (+10.001µs): return nil
                                                Task#316 ends at 51.427µs
                                                  Gather#316: index=2
                                                  Gather#316 step 1/2 (+0s): 999ns self time
                                                  Gather#316 step 2/2 (+999ns): return error
                                                  Gather#316 ends at 52.426µs
                                              Gather#323 step 7/8 (+30.449µs): 10.208µs self time
                                              Gather#323 step 8/8 (+40.657µs): return nil
                                              Gather#323 ends at 51.634µs
                                          Gather#324 step 13/14 (+1.005µs): 0s self time
                                          Gather#324 step 14/14 (+1.005µs): return nil
                                          Gather#324 ends at 10.977µs
                                      Plan#14 step 5/6 (+0s): scatter:
                                        Task#313: pool=0
                                        Task#313 step 1/2 (+0s): 9.973µs self time
                                        Task#313 step 2/2 (+9.973µs): return nil
                                        Task#313 ends at 9.973µs
                                          Gather#313: index=7
                                          Gather#313 step 1/2 (+0s): 112ns self time
                                          Gather#313 step 2/2 (+112ns): return nil
                                          Gather#313 ends at 10.085µs
                                      Plan#14 step 6/6 (+0s): ends at 6.169932ms
                                    Gather#299 step 3/4 (+6.170453ms): 477ns self time
                                    Gather#299 step 4/4 (+6.17093ms): return nil
                                    Gather#299 ends at 6.180929ms
                                Plan#13 step 4/12 (+0s): scatter:
                                  Task#291: pool=2
                                  Task#291 step 1/2 (+0s): 9.996µs self time
                                  Task#291 step 2/2 (+9.996µs): return nil
                                  Task#291 ends at 9.996µs
                                    Gather#291: index=1
                                    Gather#291 step 1/2 (+0s): 1.01µs self time
                                    Gather#291 step 2/2 (+1.01µs): return nil
                                    Gather#291 ends at 11.006µs
                                Plan#13 step 5/12 (+0s): scatter:
                                  Task#540: pool=2
                                  Task#540 step 1/2 (+0s): 3.338552ms self time
                                  Task#540 step 2/2 (+3.338552ms): return nil
                                  Task#540 ends at 3.338552ms
                                    Gather#540: index=18
                                    Gather#540 step 1/4 (+0s): 53ns self time
                                    Gather#540 step 2/4 (+53ns): scatter:
                                      Task#449: pool=0
                                      Task#449 step 1/2 (+0s): 10.205µs self time
                                      Task#449 step 2/2 (+10.205µs): return nil
                                      Task#449 ends at 3.34881ms
                                        Gather#449: index=1
                                        Gather#449 step 1/2 (+0s): 996ns self time
                                        Gather#449 step 2/2 (+996ns): return error
                                        Gather#449 ends at 3.349806ms
                                    Gather#540 step 3/4 (+53ns): 945ns self time
                                    Gather#540 step 4/4 (+998ns): return nil
                                    Gather#540 ends at 3.33955ms
                                Plan#13 step 6/12 (+0s): scatter:
                                  Task#289: pool=2
                                  Task#289 step 1/2 (+0s): 10.028µs self time
                                  Task#289 step 2/2 (+10.028µs): return error
                                  Task#289 ends at 10.028µs
                                    Combine#289: index=0 flush=<nil>
                                    Combine#289 step 1/2 (+0s): 1.001µs self time
                                    Combine#289 step 2/2 (+1.001µs): return nil
                                    Combine#289 ends at 11.029µs
                                Plan#13 step 7/12 (+0s): scatter:
                                  Task#543: pool=2
                                  Task#543 step 1/2 (+0s): 10.002µs self time
                                  Task#543 step 2/2 (+10.002µs): return nil
                                  Task#543 ends at 10.002µs
                                    Gather#543: index=0
                                    Gather#543 step 1/6 (+0s): 161ns self time
                                    Gather#543 step 2/6 (+161ns): scatter:
                                      Task#450: pool=2
                                      Task#450 step 1/2 (+0s): 9.992µs self time
                                      Task#450 step 2/2 (+9.992µs): return nil
                                      Task#450 ends at 20.155µs
                                        Gather#450: index=4
                                        Gather#450 step 1/2 (+0s): 1.007µs self time
                                        Gather#450 step 2/2 (+1.007µs): return nil
                                        Gather#450 ends at 21.162µs
                                    Gather#543 step 3/6 (+161ns): 400ns self time
                                    Gather#543 step 4/6 (+561ns): scatter:
                                      Task#293: pool=0
                                      Task#293 step 1/2 (+0s): 9.998µs self time
                                      Task#293 step 2/2 (+9.998µs): return nil
                                      Task#293 ends at 20.561µs
                                        Gather#293: index=6
                                        Gather#293 step 1/2 (+0s): 997ns self time
                                        Gather#293 step 2/2 (+997ns): return nil
                                        Gather#293 ends at 21.558µs
                                    Gather#543 step 5/6 (+561ns): 408ns self time
                                    Gather#543 step 6/6 (+969ns): return nil
                                    Gather#543 ends at 10.971µs
                                Plan#13 step 8/12 (+0s): scatter:
                                  Task#513: pool=0
                                  Task#513 step 1/2 (+0s): 6.76µs self time
                                  Task#513 step 2/2 (+6.76µs): return nil
                                  Task#513 ends at 6.76µs
                                    Gather#513: index=7
                                    Gather#513 step 1/6 (+0s): 19ns self time
                                    Gather#513 step 2/6 (+19ns): subjob:
                                      Plan#20: pathCount=18 taskCount=26 maxPathDuration=10.003446ms minGatherCount=22 maxGatherCount=30
                                         TaskPools[0]: TaskPool#58: limit=1
                                         TaskPools[1]: TaskPool#59: limit=1
                                         TaskPools[2]: TaskPool#60: limit=1
                                         TaskPools[3]: TaskPool#61: limit=2
                                         TaskPools[4]: TaskPool#62: limit=1
                                         TaskPools[5]: TaskPool#63: limit=7
                                         CombinerPools[0]: CombinerPool#112: limit=2
                                         Combiners[0]: pool=0
                                         Combiners[1]: pool=0
                                         Combiners[2]: pool=0
                                         Combiners[3]: pool=0
                                         Combiners[4]: pool=0
                                      Plan#20 step 1/5 (+0s): scatter:
                                        Task#538: pool=3
                                        Task#538 step 1/2 (+0s): 2.282µs self time
                                        Task#538 step 2/2 (+2.282µs): return nil
                                        Task#538 ends at 2.282µs
                                          Gather#538: index=2
                                          Gather#538 step 1/20 (+0s): 133ns self time
                                          Gather#538 step 2/20 (+133ns): scatter:
                                            Task#529: pool=3
                                            Task#529 step 1/2 (+0s): 10ms self time
                                            Task#529 step 2/2 (+10ms): return nil
                                            Task#529 ends at 10.002415ms
                                              Gather#529: index=2
                                              Gather#529 step 1/2 (+0s): 1.031µs self time
                                              Gather#529 step 2/2 (+1.031µs): return nil
                                              Gather#529 ends at 10.003446ms
                                          Gather#538 step 3/20 (+133ns): 96ns self time
                                          Gather#538 step 4/20 (+229ns): scatter:
                                            Task#516: pool=2
                                            Task#516 step 1/2 (+0s): 9.998µs self time
                                            Task#516 step 2/2 (+9.998µs): return nil
                                            Task#516 ends at 12.509µs
                                              Gather#516: index=1
                                              Gather#516 step 1/2 (+0s): 674.27µs self time
                                              Gather#516 step 2/2 (+674.27µs): return nil
                                              Gather#516 ends at 686.779µs
                                          Gather#538 step 5/20 (+229ns): 171ns self time
                                          Gather#538 step 6/20 (+400ns): scatter:
                                            Task#527: pool=0
                                            Task#527 step 1/2 (+0s): 10.244µs self time
                                            Task#527 step 2/2 (+10.244µs): return nil
                                            Task#527 ends at 12.926µs
                                              Gather#527: index=0
                                              Gather#527 step 1/2 (+0s): 1.091µs self time
                                              Gather#527 step 2/2 (+1.091µs): return nil
                                              Gather#527 ends at 14.017µs
                                          Gather#538 step 7/20 (+400ns): 143ns self time
                                          Gather#538 step 8/20 (+543ns): scatter:
                                            Task#537: pool=1
                                            Task#537 step 1/2 (+0s): 9.987µs self time
                                            Task#537 step 2/2 (+9.987µs): return nil
                                            Task#537 ends at 12.812µs
                                              Gather#537: index=0
                                              Gather#537 step 1/4 (+0s): 525ns self time
                                              Gather#537 step 2/4 (+525ns): scatter:
                                                Task#519: pool=3
                                                Task#519 step 1/2 (+0s): 10.194µs self time
                                                Task#519 step 2/2 (+10.194µs): return nil
                                                Task#519 ends at 23.531µs
                                                  Combine#519: index=3 flush=<nil>
                                                  Combine#519 step 1/2 (+0s): 999ns self time
                                                  Combine#519 step 2/2 (+999ns): return nil
                                                  Combine#519 ends at 24.53µs
                                              Gather#537 step 3/4 (+525ns): 368ns self time
                                              Gather#537 step 4/4 (+893ns): return nil
                                              Gather#537 ends at 13.705µs
                                          Gather#538 step 9/20 (+543ns): 442ns self time
                                          Gather#538 step 10/20 (+985ns): scatter:
                                            Task#524: pool=2
                                            Task#524 step 1/2 (+0s): 10.211µs self time
                                            Task#524 step 2/2 (+10.211µs): return nil
                                            Task#524 ends at 13.478µs
                                              Gather#524: index=1
                                              Gather#524 step 1/2 (+0s): 1.085µs self time
                                              Gather#524 step 2/2 (+1.085µs): return nil
                                              Gather#524 ends at 14.563µs
                                          Gather#538 step 11/20 (+985ns): 71ns self time
                                          Gather#538 step 12/20 (+1.056µs): scatter:
                                            Task#535: pool=5
                                            Task#535 step 1/2 (+0s): 11.687µs self time
                                            Task#535 step 2/2 (+11.687µs): return error
                                            Task#535 ends at 15.025µs
                                              Gather#535: index=2
                                              Gather#535 step 1/6 (+0s): 262.616µs self time
                                              Gather#535 step 2/6 (+262.616µs): scatter:
                                                Task#531: pool=1
                                                Task#531 step 1/2 (+0s): 6.293µs self time
                                                Task#531 step 2/2 (+6.293µs): return error
                                                Task#531 ends at 283.934µs
                                                  Combine#531: index=2 flush=<nil>
                                                  Combine#531 step 1/2 (+0s): 1.001µs self time
                                                  Combine#531 step 2/2 (+1.001µs): return nil
                                                  Combine#531 ends at 284.935µs
                                              Gather#535 step 3/6 (+262.616µs): 263.614µs self time
                                              Gather#535 step 4/6 (+526.23µs): scatter:
                                                Task#534: pool=5
                                                Task#534 step 1/2 (+0s): 9.96µs self time
                                                Task#534 step 2/2 (+9.96µs): return nil
                                                Task#534 ends at 551.215µs
                                                  Gather#534: index=0
                                                  Gather#534 step 1/10 (+0s): 133ns self time
                                                  Gather#534 step 2/10 (+133ns): scatter:
                                                    Task#533: pool=4
                                                    Task#533 step 1/2 (+0s): 10.318µs self time
                                                    Task#533 step 2/2 (+10.318µs): return nil
                                                    Task#533 ends at 561.666µs
                                                      Gather#533: index=2
                                                      Gather#533 step 1/6 (+0s): 294ns self time
                                                      Gather#533 step 2/6 (+294ns): scatter:
                                                        Task#518: pool=2
                                                        Task#518 step 1/2 (+0s): 10.001µs self time
                                                        Task#518 step 2/2 (+10.001µs): return nil
                                                        Task#518 ends at 571.961µs
                                                          Combine#518: index=0 flush=Gather#518
                                                          Combine#518 step 1/2 (+0s): 1.989µs self time
                                                          Combine#518 step 2/2 (+1.989µs): return nil
                                                          Combine#518 ends at 573.95µs
                                                            Gather#518: index=0
                                                            Gather#518 step 1/2 (+0s): 1.011µs self time
                                                            Gather#518 step 2/2 (+1.011µs): return nil
                                                            Gather#518 ends at 0s
                                                      Gather#533 step 3/6 (+294ns): 48ns self time
                                                      Gather#533 step 4/6 (+342ns): scatter:
                                                        Task#520: pool=2
                                                        Task#520 step 1/2 (+0s): 10.113µs self time
                                                        Task#520 step 2/2 (+10.113µs): return nil
                                                        Task#520 ends at 572.121µs
                                                          Combine#520: index=1 flush=Gather#520
                                                          Combine#520 step 1/2 (+0s): 999ns self time
                                                          Combine#520 step 2/2 (+999ns): return nil
                                                          Combine#520 ends at 573.12µs
                                                            Gather#520: index=0
                                                            Gather#520 step 1/2 (+0s): 998ns self time
                                                            Gather#520 step 2/2 (+998ns): return nil
                                                            Gather#520 ends at 0s
                                                      Gather#533 step 5/6 (+342ns): 540ns self time
                                                      Gather#533 step 6/6 (+882ns): return nil
                                                      Gather#533 ends at 562.548µs
                                                  Gather#534 step 3/10 (+133ns): 146ns self time
                                                  Gather#534 step 4/10 (+279ns): scatter:
                                                    Task#532: pool=0
                                                    Task#532 step 1/2 (+0s): 10.005µs self time
                                                    Task#532 step 2/2 (+10.005µs): return nil
                                                    Task#532 ends at 561.499µs
                                                      Gather#532: index=3
                                                      Gather#532 step 1/4 (+0s): 120.538µs self time
                                                      Gather#532 step 2/4 (+120.538µs): scatter:
                                                        Task#515: pool=5
                                                        Task#515 step 1/2 (+0s): 10.716µs self time
                                                        Task#515 step 2/2 (+10.716µs): return nil
                                                        Task#515 ends at 692.753µs
                                                          Combine#515: index=0 flush=<nil>
                                                          Combine#515 step 1/2 (+0s): 1.14µs self time
                                                          Combine#515 step 2/2 (+1.14µs): return nil
                                                          Combine#515 ends at 693.893µs
                                                      Gather#532 step 3/4 (+120.538µs): 115.596µs self time
                                                      Gather#532 step 4/4 (+236.134µs): return nil
                                                      Gather#532 ends at 797.633µs
                                                  Gather#534 step 5/10 (+279ns): 259ns self time
                                                  Gather#534 step 6/10 (+538ns): scatter:
                                                    Task#526: pool=1
                                                    Task#526 step 1/2 (+0s): 1.285072ms self time
                                                    Task#526 step 2/2 (+1.285072ms): return nil
                                                    Task#526 ends at 1.836825ms
                                                      Gather#526: index=1
                                                      Gather#526 step 1/2 (+0s): 1.069µs self time
                                                      Gather#526 step 2/2 (+1.069µs): return nil
                                                      Gather#526 ends at 1.837894ms
                                                  Gather#534 step 7/10 (+538ns): 66ns self time
                                                  Gather#534 step 8/10 (+604ns): scatter:
                                                    Task#517: pool=5
                                                    Task#517 step 1/2 (+0s): 10.001µs self time
                                                    Task#517 step 2/2 (+10.001µs): return nil
                                                    Task#517 ends at 561.82µs
                                                      Gather#517: index=2
                                                      Gather#517 step 1/2 (+0s): 11ns self time
                                                      Gather#517 step 2/2 (+11ns): return error
                                                      Gather#517 ends at 561.831µs
                                                  Gather#534 step 9/10 (+604ns): 63ns self time
                                                  Gather#534 step 10/10 (+667ns): return nil
                                                  Gather#534 ends at 551.882µs
                                              Gather#535 step 5/6 (+526.23µs): 263.249µs self time
                                              Gather#535 step 6/6 (+789.479µs): return nil
                                              Gather#535 ends at 804.504µs
                                          Gather#538 step 13/20 (+1.056µs): 259ns self time
                                          Gather#538 step 14/20 (+1.315µs): scatter:
                                            Task#530: pool=5
                                            Task#530 step 1/2 (+0s): 9.995µs self time
                                            Task#530 step 2/2 (+9.995µs): return nil
                                            Task#530 ends at 13.592µs
                                              Gather#530: index=1
                                              Gather#530 step 1/2 (+0s): 822ns self time
                                              Gather#530 step 2/2 (+822ns): return nil
                                              Gather#530 ends at 14.414µs
                                          Gather#538 step 15/20 (+1.315µs): 0s self time
                                          Gather#538 step 16/20 (+1.315µs): scatter:
                                            Task#514: pool=3
                                            Task#514 step 1/2 (+0s): 9.993µs self time
                                            Task#514 step 2/2 (+9.993µs): return error
                                            Task#514 ends at 13.59µs
                                              Gather#514: index=0
                                              Gather#514 step 1/2 (+0s): 999ns self time
                                              Gather#514 step 2/2 (+999ns): return nil
                                              Gather#514 ends at 14.589µs
                                          Gather#538 step 17/20 (+1.315µs): 0s self time
                                          Gather#538 step 18/20 (+1.315µs): scatter:
                                            Task#522: pool=2
                                            Task#522 step 1/2 (+0s): 6.547715ms self time
                                            Task#522 step 2/2 (+6.547715ms): return nil
                                            Task#522 ends at 6.551312ms
                                              Gather#522: index=1
                                              Gather#522 step 1/2 (+0s): 22ns self time
                                              Gather#522 step 2/2 (+22ns): return nil
                                              Gather#522 ends at 6.551334ms
                                          Gather#538 step 19/20 (+1.315µs): 0s self time
                                          Gather#538 step 20/20 (+1.315µs): return nil
                                          Gather#538 ends at 3.597µs
                                      Plan#20 step 2/5 (+0s): scatter:
                                        Task#523: pool=5
                                        Task#523 step 1/2 (+0s): 10.157µs self time
                                        Task#523 step 2/2 (+10.157µs): return nil
                                        Task#523 ends at 10.157µs
                                          Gather#523: index=1
                                          Gather#523 step 1/2 (+0s): 999ns self time
                                          Gather#523 step 2/2 (+999ns): return error
                                          Gather#523 ends at 11.156µs
                                      Plan#20 step 3/5 (+0s): scatter:
                                        Task#539: pool=4
                                        Task#539 step 1/2 (+0s): 4.547021ms self time
                                        Task#539 step 2/2 (+4.547021ms): return nil
                                        Task#539 ends at 4.547021ms
                                          Combine#539: index=3 flush=<nil>
                                          Combine#539 step 1/4 (+0s): 312.338µs self time
                                          Combine#539 step 2/4 (+312.338µs): scatter:
                                            Task#536: pool=5
                                            Task#536 step 1/2 (+0s): 10.003µs self time
                                            Task#536 step 2/2 (+10.003µs): return nil
                                            Task#536 ends at 4.869362ms
                                              Gather#536: index=2
                                              Gather#536 step 1/6 (+0s): 168ns self time
                                              Gather#536 step 2/6 (+168ns): scatter:
                                                Task#528: pool=3
                                                Task#528 step 1/2 (+0s): 9.014µs self time
                                                Task#528 step 2/2 (+9.014µs): return nil
                                                Task#528 ends at 4.878544ms
                                                  Gather#528: index=1
                                                  Gather#528 step 1/2 (+0s): 4.964µs self time
                                                  Gather#528 step 2/2 (+4.964µs): return nil
                                                  Gather#528 ends at 4.883508ms
                                              Gather#536 step 3/6 (+168ns): 442ns self time
                                              Gather#536 step 4/6 (+610ns): scatter:
                                                Task#521: pool=0
                                                Task#521 step 1/2 (+0s): 10.014µs self time
                                                Task#521 step 2/2 (+10.014µs): return nil
                                                Task#521 ends at 4.879986ms
                                                  Gather#521: index=0
                                                  Gather#521 step 1/2 (+0s): 251ns self time
                                                  Gather#521 step 2/2 (+251ns): return nil
                                                  Gather#521 ends at 4.880237ms
                                              Gather#536 step 5/6 (+610ns): 385ns self time
                                              Gather#536 step 6/6 (+995ns): return nil
                                              Gather#536 ends at 4.870357ms
                                          Combine#539 step 3/4 (+312.338µs): 505.815µs self time
                                          Combine#539 step 4/4 (+818.153µs): return nil
                                          Combine#539 ends at 5.365174ms
                                      Plan#20 step 4/5 (+0s): scatter:
                                        Task#525: pool=2
                                        Task#525 step 1/2 (+0s): 9.999µs self time
                                        Task#525 step 2/2 (+9.999µs): return nil
                                        Task#525 ends at 9.999µs
                                          Gather#525: index=2
                                          Gather#525 step 1/2 (+0s): 1.59µs self time
                                          Gather#525 step 2/2 (+1.59µs): return nil
                                          Gather#525 ends at 11.589µs
                                      Plan#20 step 5/5 (+0s): ends at 10.003446ms
                                    Gather#513 step 3/6 (+10.003465ms): 24ns self time
                                    Gather#513 step 4/6 (+10.003489ms): scatter:
                                      Task#292: pool=0
                                      Task#292 step 1/2 (+0s): 9.998µs self time
                                      Task#292 step 2/2 (+9.998µs): return nil
                                      Task#292 ends at 10.020247ms
                                        Gather#292: index=6
                                        Gather#292 step 1/2 (+0s): 1µs self time
                                        Gather#292 step 2/2 (+1µs): return nil
                                        Gather#292 ends at 10.021247ms
                                    Gather#513 step 5/6 (+10.003489ms): 20ns self time
                                    Gather#513 step 6/6 (+10.003509ms): return nil
                                    Gather#513 ends at 10.010269ms
                                Plan#13 step 9/12 (+0s): scatter:
                                  Task#328: pool=0
                                  Task#328 step 1/4 (+0s): 522.111µs self time
                                  Task#328 step 2/4 (+522.111µs): subjob:
                                    Plan#16: pathCount=15 taskCount=28 maxPathDuration=11.066734ms minGatherCount=22 maxGatherCount=32
                                       TaskPools[0]: TaskPool#50: limit=2
                                       TaskPools[1]: TaskPool#51: limit=2
                                       TaskPools[2]: TaskPool#52: limit=2
                                       TaskPools[3]: TaskPool#53: limit=2
                                       CombinerPools[0]: CombinerPool#82: limit=2
                                       CombinerPools[1]: CombinerPool#83: limit=2
                                       CombinerPools[2]: CombinerPool#84: limit=5
                                       CombinerPools[3]: CombinerPool#85: limit=1
                                       Combiners[0]: pool=0
                                       Combiners[1]: pool=1
                                       Combiners[2]: pool=1
                                       Combiners[3]: pool=3
                                       Combiners[4]: pool=3
                                       Combiners[5]: pool=3
                                    Plan#16 step 1/7 (+0s): scatter:
                                      Task#376: pool=3
                                      Task#376 step 1/2 (+0s): 5.713447ms self time
                                      Task#376 step 2/2 (+5.713447ms): return nil
                                      Task#376 ends at 5.713447ms
                                        Gather#376: index=0
                                        Gather#376 step 1/4 (+0s): 515ns self time
                                        Gather#376 step 2/4 (+515ns): scatter:
                                          Task#369: pool=3
                                          Task#369 step 1/2 (+0s): 9.999µs self time
                                          Task#369 step 2/2 (+9.999µs): return error
                                          Task#369 ends at 5.723961ms
                                            Gather#369: index=0
                                            Gather#369 step 1/4 (+0s): 2.569µs self time
                                            Gather#369 step 2/4 (+2.569µs): scatter:
                                              Task#368: pool=0
                                              Task#368 step 1/2 (+0s): 10.003µs self time
                                              Task#368 step 2/2 (+10.003µs): return nil
                                              Task#368 ends at 5.736533ms
                                                Gather#368: index=0
                                                Gather#368 step 1/6 (+0s): 520ns self time
                                                Gather#368 step 2/6 (+520ns): scatter:
                                                  Task#360: pool=2
                                                  Task#360 step 1/2 (+0s): 9.987µs self time
                                                  Task#360 step 2/2 (+9.987µs): return nil
                                                  Task#360 ends at 5.74704ms
                                                    Gather#360: index=0
                                                    Gather#360 step 1/2 (+0s): 1µs self time
                                                    Gather#360 step 2/2 (+1µs): return nil
                                                    Gather#360 ends at 5.74804ms
                                                Gather#368 step 3/6 (+520ns): 157ns self time
                                                Gather#368 step 4/6 (+677ns): scatter:
                                                  Task#364: pool=3
                                                  Task#364 step 1/2 (+0s): 9.995µs self time
                                                  Task#364 step 2/2 (+9.995µs): return nil
                                                  Task#364 ends at 5.747205ms
                                                    Gather#364: index=0
                                                    Gather#364 step 1/4 (+0s): 500ns self time
                                                    Gather#364 step 2/4 (+500ns): scatter:
                                                      Task#352: pool=3
                                                      Task#352 step 1/2 (+0s): 10.007µs self time
                                                      Task#352 step 2/2 (+10.007µs): return nil
                                                      Task#352 ends at 5.757712ms
                                                        Gather#352: index=0
                                                        Gather#352 step 1/2 (+0s): 913.572µs self time
                                                        Gather#352 step 2/2 (+913.572µs): return nil
                                                        Gather#352 ends at 6.671284ms
                                                    Gather#364 step 3/4 (+500ns): 498ns self time
                                                    Gather#364 step 4/4 (+998ns): return nil
                                                    Gather#364 ends at 5.748203ms
                                                Gather#368 step 5/6 (+677ns): 183ns self time
                                                Gather#368 step 6/6 (+860ns): return nil
                                                Gather#368 ends at 5.737393ms
                                            Gather#369 step 3/4 (+2.569µs): 868ns self time
                                            Gather#369 step 4/4 (+3.437µs): return nil
                                            Gather#369 ends at 5.727398ms
                                        Gather#376 step 3/4 (+515ns): 513ns self time
                                        Gather#376 step 4/4 (+1.028µs): return nil
                                        Gather#376 ends at 5.714475ms
                                    Plan#16 step 2/7 (+0s): scatter:
                                      Task#375: pool=0
                                      Task#375 step 1/2 (+0s): 9.021µs self time
                                      Task#375 step 2/2 (+9.021µs): return nil
                                      Task#375 ends at 9.021µs
                                        Gather#375: index=0
                                        Gather#375 step 1/8 (+0s): 1.796µs self time
                                        Gather#375 step 2/8 (+1.796µs): scatter:
                                          Task#358: pool=0
                                          Task#358 step 1/2 (+0s): 2.613µs self time
                                          Task#358 step 2/2 (+2.613µs): return nil
                                          Task#358 ends at 13.43µs
                                            Gather#358: index=0
                                            Gather#358 step 1/2 (+0s): 2.937µs self time
                                            Gather#358 step 2/2 (+2.937µs): return nil
                                            Gather#358 ends at 16.367µs
                                        Gather#375 step 3/8 (+1.796µs): 1.777µs self time
                                        Gather#375 step 4/8 (+3.573µs): scatter:
                                          Task#371: pool=3
                                          Task#371 step 1/2 (+0s): 7.593µs self time
                                          Task#371 step 2/2 (+7.593µs): return nil
                                          Task#371 ends at 20.187µs
                                            Combine#371: index=1 flush=Gather#371
                                            Combine#371 step 1/2 (+0s): 1.008µs self time
                                            Combine#371 step 2/2 (+1.008µs): return nil
                                            Combine#371 ends at 21.195µs
                                              Gather#371: index=0
                                              Gather#371 step 1/4 (+0s): 950ns self time
                                              Gather#371 step 2/4 (+950ns): scatter:
                                                Task#349: pool=2
                                                Task#349 step 1/2 (+0s): 10.006µs self time
                                                Task#349 step 2/2 (+10.006µs): return nil
                                                Task#349 ends at 0s
                                                  Gather#349: index=0
                                                  Gather#349 step 1/2 (+0s): 998ns self time
                                                  Gather#349 step 2/2 (+998ns): return nil
                                                  Gather#349 ends at 0s
                                              Gather#371 step 3/4 (+950ns): 50ns self time
                                              Gather#371 step 4/4 (+1µs): return nil
                                              Gather#371 ends at 0s
                                        Gather#375 step 5/8 (+3.573µs): 2.379µs self time
                                        Gather#375 step 6/8 (+5.952µs): scatter:
                                          Task#373: pool=0
                                          Task#373 step 1/2 (+0s): 9.998µs self time
                                          Task#373 step 2/2 (+9.998µs): return nil
                                          Task#373 ends at 24.971µs
                                            Combine#373: index=2 flush=<nil>
                                            Combine#373 step 1/6 (+0s): 241ns self time
                                            Combine#373 step 2/6 (+241ns): scatter:
                                              Task#354: pool=1
                                              Task#354 step 1/2 (+0s): 56.049µs self time
                                              Task#354 step 2/2 (+56.049µs): return nil
                                              Task#354 ends at 81.261µs
                                                Combine#354: index=4 flush=<nil>
                                                Combine#354 step 1/2 (+0s): 1.223µs self time
                                                Combine#354 step 2/2 (+1.223µs): return nil
                                                Combine#354 ends at 82.484µs
                                            Combine#373 step 3/6 (+241ns): 242ns self time
                                            Combine#373 step 4/6 (+483ns): scatter:
                                              Task#363: pool=0
                                              Task#363 step 1/2 (+0s): 10.009µs self time
                                              Task#363 step 2/2 (+10.009µs): return nil
                                              Task#363 ends at 35.463µs
                                                Combine#363: index=5 flush=<nil>
                                                Combine#363 step 1/2 (+0s): 998ns self time
                                                Combine#363 step 2/2 (+998ns): return nil
                                                Combine#363 ends at 36.461µs
                                            Combine#373 step 5/6 (+483ns): 236ns self time
                                            Combine#373 step 6/6 (+719ns): return nil
                                            Combine#373 ends at 25.69µs
                                        Gather#375 step 7/8 (+5.952µs): 1.24µs self time
                                        Gather#375 step 8/8 (+7.192µs): return nil
                                        Gather#375 ends at 16.213µs
                                    Plan#16 step 3/7 (+0s): scatter:
                                      Task#355: pool=3
                                      Task#355 step 1/2 (+0s): 10.941µs self time
                                      Task#355 step 2/2 (+10.941µs): return nil
                                      Task#355 ends at 10.941µs
                                        Gather#355: index=0
                                        Gather#355 step 1/2 (+0s): 1.502µs self time
                                        Gather#355 step 2/2 (+1.502µs): return error
                                        Gather#355 ends at 12.443µs
                                    Plan#16 step 4/7 (+0s): scatter:
                                      Task#353: pool=3
                                      Task#353 step 1/2 (+0s): 10.015µs self time
                                      Task#353 step 2/2 (+10.015µs): return nil
                                      Task#353 ends at 10.015µs
                                        Gather#353: index=0
                                        Gather#353 step 1/2 (+0s): 107.427µs self time
                                        Gather#353 step 2/2 (+107.427µs): return nil
                                        Gather#353 ends at 117.442µs
                                    Plan#16 step 5/7 (+0s): scatter:
                                      Task#374: pool=0
                                      Task#374 step 1/2 (+0s): 9.981µs self time
                                      Task#374 step 2/2 (+9.981µs): return nil
                                      Task#374 ends at 9.981µs
                                        Gather#374: index=0
                                        Gather#374 step 1/8 (+0s): 262ns self time
                                        Gather#374 step 2/8 (+262ns): scatter:
                                          Task#361: pool=0
                                          Task#361 step 1/2 (+0s): 10.007µs self time
                                          Task#361 step 2/2 (+10.007µs): return nil
                                          Task#361 ends at 20.25µs
                                            Gather#361: index=0
                                            Gather#361 step 1/2 (+0s): 999ns self time
                                            Gather#361 step 2/2 (+999ns): return nil
                                            Gather#361 ends at 21.249µs
                                        Gather#374 step 3/8 (+262ns): 137ns self time
                                        Gather#374 step 4/8 (+399ns): scatter:
                                          Task#370: pool=0
                                          Task#370 step 1/2 (+0s): 9.459µs self time
                                          Task#370 step 2/2 (+9.459µs): return nil
                                          Task#370 ends at 19.839µs
                                            Gather#370: index=0
                                            Gather#370 step 1/4 (+0s): 688ns self time
                                            Gather#370 step 2/4 (+688ns): scatter:
                                              Task#351: pool=1
                                              Task#351 step 1/2 (+0s): 10ms self time
                                              Task#351 step 2/2 (+10ms): return nil
                                              Task#351 ends at 10.020527ms
                                                Gather#351: index=0
                                                Gather#351 step 1/2 (+0s): 1.031µs self time
                                                Gather#351 step 2/2 (+1.031µs): return nil
                                                Gather#351 ends at 10.021558ms
                                            Gather#370 step 3/4 (+688ns): 293ns self time
                                            Gather#370 step 4/4 (+981ns): return nil
                                            Gather#370 ends at 20.82µs
                                        Gather#374 step 5/8 (+399ns): 5ns self time
                                        Gather#374 step 6/8 (+404ns): scatter:
                                          Task#372: pool=2
                                          Task#372 step 1/2 (+0s): 1.045392ms self time
                                          Task#372 step 2/2 (+1.045392ms): return nil
                                          Task#372 ends at 1.055777ms
                                            Combine#372: index=0 flush=<nil>
                                            Combine#372 step 1/6 (+0s): 0s self time
                                            Combine#372 step 2/6 (+0s): scatter:
                                              Task#367: pool=1
                                              Task#367 step 1/2 (+0s): 9.162µs self time
                                              Task#367 step 2/2 (+9.162µs): return nil
                                              Task#367 ends at 1.064939ms
                                                Combine#367: index=1 flush=<nil>
                                                Combine#367 step 1/8 (+0s): 296ns self time
                                                Combine#367 step 2/8 (+296ns): scatter:
                                                  Task#366: pool=3
                                                  Task#366 step 1/2 (+0s): 9.98µs self time
                                                  Task#366 step 2/2 (+9.98µs): return nil
                                                  Task#366 ends at 1.075215ms
                                                    Combine#366: index=1 flush=<nil>
                                                    Combine#366 step 1/4 (+0s): 628ns self time
                                                    Combine#366 step 2/4 (+628ns): scatter:
                                                      Task#357: pool=0
                                                      Task#357 step 1/2 (+0s): 10.001µs self time
                                                      Task#357 step 2/2 (+10.001µs): return error
                                                      Task#357 ends at 1.085844ms
                                                        Gather#357: index=0
                                                        Gather#357 step 1/2 (+0s): 2.413µs self time
                                                        Gather#357 step 2/2 (+2.413µs): return nil
                                                        Gather#357 ends at 1.088257ms
                                                    Combine#366 step 3/4 (+628ns): 374ns self time
                                                    Combine#366 step 4/4 (+1.002µs): return nil
                                                    Combine#366 ends at 1.076217ms
                                                Combine#367 step 3/8 (+296ns): 257ns self time
                                                Combine#367 step 4/8 (+553ns): scatter:
                                                  Task#365: pool=0
                                                  Task#365 step 1/2 (+0s): 9.994µs self time
                                                  Task#365 step 2/2 (+9.994µs): return nil
                                                  Task#365 ends at 1.075486ms
                                                    Gather#365: index=0
                                                    Gather#365 step 1/4 (+0s): 1.48µs self time
                                                    Gather#365 step 2/4 (+1.48µs): scatter:
                                                      Task#359: pool=0
                                                      Task#359 step 1/2 (+0s): 10.931µs self time
                                                      Task#359 step 2/2 (+10.931µs): return nil
                                                      Task#359 ends at 1.087897ms
                                                        Gather#359: index=0
                                                        Gather#359 step 1/2 (+0s): 990ns self time
                                                        Gather#359 step 2/2 (+990ns): return nil
                                                        Gather#359 ends at 1.088887ms
                                                    Gather#365 step 3/4 (+1.48µs): 480ns self time
                                                    Gather#365 step 4/4 (+1.96µs): return nil
                                                    Gather#365 ends at 1.077446ms
                                                Combine#367 step 5/8 (+553ns): 234ns self time
                                                Combine#367 step 6/8 (+787ns): scatter:
                                                  Task#350: pool=3
                                                  Task#350 step 1/2 (+0s): 10ms self time
                                                  Task#350 step 2/2 (+10ms): return nil
                                                  Task#350 ends at 11.065726ms
                                                    Gather#350: index=0
                                                    Gather#350 step 1/2 (+0s): 1.008µs self time
                                                    Gather#350 step 2/2 (+1.008µs): return nil
                                                    Gather#350 ends at 11.066734ms
                                                Combine#367 step 7/8 (+787ns): 215ns self time
                                                Combine#367 step 8/8 (+1.002µs): return nil
                                                Combine#367 ends at 1.065941ms
                                            Combine#372 step 3/6 (+0s): 252ns self time
                                            Combine#372 step 4/6 (+252ns): scatter:
                                              Task#362: pool=2
                                              Task#362 step 1/2 (+0s): 10.054µs self time
                                              Task#362 step 2/2 (+10.054µs): return error
                                              Task#362 ends at 1.066083ms
                                                Gather#362: index=0
                                                Gather#362 step 1/2 (+0s): 1.656µs self time
                                                Gather#362 step 2/2 (+1.656µs): return nil
                                                Gather#362 ends at 1.067739ms
                                            Combine#372 step 5/6 (+252ns): 377ns self time
                                            Combine#372 step 6/6 (+629ns): return nil
                                            Combine#372 ends at 1.056406ms
                                        Gather#374 step 7/8 (+404ns): 594ns self time
                                        Gather#374 step 8/8 (+998ns): return nil
                                        Gather#374 ends at 10.979µs
                                    Plan#16 step 6/7 (+0s): scatter:
                                      Task#356: pool=1
                                      Task#356 step 1/2 (+0s): 9.999µs self time
                                      Task#356 step 2/2 (+9.999µs): return nil
                                      Task#356 ends at 9.999µs
                                        Gather#356: index=0
                                        Gather#356 step 1/2 (+0s): 985ns self time
                                        Gather#356 step 2/2 (+985ns): return nil
                                        Gather#356 ends at 10.984µs
                                    Plan#16 step 7/7 (+0s): ends at 11.066734ms
                                  Task#328 step 3/4 (+11.588845ms): 522.138µs self time
                                  Task#328 step 4/4 (+12.110983ms): return nil
                                  Task#328 ends at 12.110983ms
                                    Gather#328: index=2
                                    Gather#328 step 1/4 (+0s): 666ns self time
                                    Gather#328 step 2/4 (+666ns): subjob:
                                      Plan#15: pathCount=12 taskCount=20 maxPathDuration=7.941428ms minGatherCount=15 maxGatherCount=57
                                         TaskPools[0]: TaskPool#45: limit=2
                                         TaskPools[1]: TaskPool#46: limit=2
                                         TaskPools[2]: TaskPool#47: limit=10
                                         TaskPools[3]: TaskPool#48: limit=2
                                         TaskPools[4]: TaskPool#49: limit=1
                                         CombinerPools[0]: CombinerPool#76: limit=10
                                         CombinerPools[1]: CombinerPool#77: limit=2
                                         CombinerPools[2]: CombinerPool#78: limit=8
                                         CombinerPools[3]: CombinerPool#79: limit=6
                                         CombinerPools[4]: CombinerPool#80: limit=6
                                         CombinerPools[5]: CombinerPool#81: limit=2
                                         Combiners[0]: pool=3
                                         Combiners[1]: pool=0
                                      Plan#15 step 1/6 (+0s): scatter:
                                        Task#338: pool=0
                                        Task#338 step 1/2 (+0s): 2.445µs self time
                                        Task#338 step 2/2 (+2.445µs): return nil
                                        Task#338 ends at 2.445µs
                                          Gather#338: index=0
                                          Gather#338 step 1/2 (+0s): 129ns self time
                                          Gather#338 step 2/2 (+129ns): return nil
                                          Gather#338 ends at 2.574µs
                                      Plan#15 step 2/6 (+0s): scatter:
                                        Task#333: pool=0
                                        Task#333 step 1/2 (+0s): 10.17µs self time
                                        Task#333 step 2/2 (+10.17µs): return nil
                                        Task#333 ends at 10.17µs
                                          Combine#333: index=0 flush=<nil>
                                          Combine#333 step 1/2 (+0s): 1µs self time
                                          Combine#333 step 2/2 (+1µs): return nil
                                          Combine#333 ends at 11.17µs
                                      Plan#15 step 3/6 (+0s): scatter:
                                        Task#347: pool=3
                                        Task#347 step 1/2 (+0s): 10.027µs self time
                                        Task#347 step 2/2 (+10.027µs): return nil
                                        Task#347 ends at 10.027µs
                                          Gather#347: index=2
                                          Gather#347 step 1/4 (+0s): 820ns self time
                                          Gather#347 step 2/4 (+820ns): scatter:
                                            Task#346: pool=1
                                            Task#346 step 1/2 (+0s): 8.836µs self time
                                            Task#346 step 2/2 (+8.836µs): return nil
                                            Task#346 ends at 19.683µs
                                              Gather#346: index=2
                                              Gather#346 step 1/6 (+0s): 338ns self time
                                              Gather#346 step 2/6 (+338ns): scatter:
                                                Task#329: pool=3
                                                Task#329 step 1/2 (+0s): 10.001µs self time
                                                Task#329 step 2/2 (+10.001µs): return nil
                                                Task#329 ends at 30.022µs
                                                  Gather#329: index=1
                                                  Gather#329 step 1/2 (+0s): 237.54µs self time
                                                  Gather#329 step 2/2 (+237.54µs): return nil
                                                  Gather#329 ends at 267.562µs
                                              Gather#346 step 3/6 (+338ns): 337ns self time
                                              Gather#346 step 4/6 (+675ns): scatter:
                                                Task#343: pool=3
                                                Task#343 step 1/2 (+0s): 10.003µs self time
                                                Task#343 step 2/2 (+10.003µs): return nil
                                                Task#343 ends at 30.361µs
                                                  Gather#343: index=2
                                                  Gather#343 step 1/4 (+0s): 488ns self time
                                                  Gather#343 step 2/4 (+488ns): scatter:
                                                    Task#340: pool=2
                                                    Task#340 step 1/2 (+0s): 9.966µs self time
                                                    Task#340 step 2/2 (+9.966µs): return nil
                                                    Task#340 ends at 40.815µs
                                                      Combine#340: index=0 flush=<nil>
                                                      Combine#340 step 1/2 (+0s): 982ns self time
                                                      Combine#340 step 2/2 (+982ns): return nil
                                                      Combine#340 ends at 41.797µs
                                                  Gather#343 step 3/4 (+488ns): 484ns self time
                                                  Gather#343 step 4/4 (+972ns): return nil
                                                  Gather#343 ends at 31.333µs
                                              Gather#346 step 5/6 (+675ns): 339ns self time
                                              Gather#346 step 6/6 (+1.014µs): return nil
                                              Gather#346 ends at 20.697µs
                                          Gather#347 step 3/4 (+820ns): 0s self time
                                          Gather#347 step 4/4 (+820ns): return nil
                                          Gather#347 ends at 10.847µs
                                      Plan#15 step 4/6 (+0s): scatter:
                                        Task#348: pool=2
                                        Task#348 step 1/2 (+0s): 2.241µs self time
                                        Task#348 step 2/2 (+2.241µs): return nil
                                        Task#348 ends at 2.241µs
                                          Gather#348: index=0
                                          Gather#348 step 1/6 (+0s): 0s self time
                                          Gather#348 step 2/6 (+0s): scatter:
                                            Task#331: pool=0
                                            Task#331 step 1/2 (+0s): 9.852µs self time
                                            Task#331 step 2/2 (+9.852µs): return nil
                                            Task#331 ends at 12.093µs
                                              Gather#331: index=1
                                              Gather#331 step 1/2 (+0s): 830ns self time
                                              Gather#331 step 2/2 (+830ns): return nil
                                              Gather#331 ends at 12.923µs
                                          Gather#348 step 3/6 (+0s): 0s self time
                                          Gather#348 step 4/6 (+0s): scatter:
                                            Task#345: pool=3
                                            Task#345 step 1/2 (+0s): 12.791µs self time
                                            Task#345 step 2/2 (+12.791µs): return nil
                                            Task#345 ends at 15.032µs
                                              Gather#345: index=1
                                              Gather#345 step 1/4 (+0s): 499.988µs self time
                                              Gather#345 step 2/4 (+499.988µs): scatter:
                                                Task#344: pool=4
                                                Task#344 step 1/2 (+0s): 10.993µs self time
                                                Task#344 step 2/2 (+10.993µs): return nil
                                                Task#344 ends at 526.013µs
                                                  Combine#344: index=1 flush=<nil>
                                                  Combine#344 step 1/12 (+0s): 166ns self time
                                                  Combine#344 step 2/12 (+166ns): scatter:
                                                    Task#332: pool=2
                                                    Task#332 step 1/2 (+0s): 9.999µs self time
                                                    Task#332 step 2/2 (+9.999µs): return nil
                                                    Task#332 ends at 536.178µs
                                                      Gather#332: index=0
                                                      Gather#332 step 1/2 (+0s): 1.001µs self time
                                                      Gather#332 step 2/2 (+1.001µs): return nil
                                                      Gather#332 ends at 537.179µs
                                                  Combine#344 step 3/12 (+166ns): 232ns self time
                                                  Combine#344 step 4/12 (+398ns): scatter:
                                                    Task#342: pool=3
                                                    Task#342 step 1/2 (+0s): 9.769µs self time
                                                    Task#342 step 2/2 (+9.769µs): return nil
                                                    Task#342 ends at 536.18µs
                                                      Gather#342: index=2
                                                      Gather#342 step 1/4 (+0s): 641ns self time
                                                      Gather#342 step 2/4 (+641ns): scatter:
                                                        Task#336: pool=2
                                                        Task#336 step 1/2 (+0s): 10.006µs self time
                                                        Task#336 step 2/2 (+10.006µs): return nil
                                                        Task#336 ends at 546.827µs
                                                          Combine#336: index=1 flush=<nil>
                                                          Combine#336 step 1/2 (+0s): 255ns self time
                                                          Combine#336 step 2/2 (+255ns): return nil
                                                          Combine#336 ends at 547.082µs
                                                      Gather#342 step 3/4 (+641ns): 377ns self time
                                                      Gather#342 step 4/4 (+1.018µs): return nil
                                                      Gather#342 ends at 537.198µs
                                                  Combine#344 step 5/12 (+398ns): 135ns self time
                                                  Combine#344 step 6/12 (+533ns): scatter:
                                                    Task#335: pool=4
                                                    Task#335 step 1/2 (+0s): 10.029µs self time
                                                    Task#335 step 2/2 (+10.029µs): return nil
                                                    Task#335 ends at 536.575µs
                                                      Gather#335: index=2
                                                      Gather#335 step 1/2 (+0s): 998ns self time
                                                      Gather#335 step 2/2 (+998ns): return nil
                                                      Gather#335 ends at 537.573µs
                                                  Combine#344 step 7/12 (+533ns): 154ns self time
                                                  Combine#344 step 8/12 (+687ns): scatter:
                                                    Task#341: pool=2
                                                    Task#341 step 1/2 (+0s): 9.934µs self time
                                                    Task#341 step 2/2 (+9.934µs): return nil
                                                    Task#341 ends at 536.634µs
                                                      Gather#341: index=1
                                                      Gather#341 step 1/6 (+0s): 353ns self time
                                                      Gather#341 step 2/6 (+353ns): scatter:
                                                        Task#337: pool=0
                                                        Task#337 step 1/2 (+0s): 7.40344ms self time
                                                        Task#337 step 2/2 (+7.40344ms): return nil
                                                        Task#337 ends at 7.940427ms
                                                          Combine#337: index=1 flush=<nil>
                                                          Combine#337 step 1/2 (+0s): 1.001µs self time
                                                          Combine#337 step 2/2 (+1.001µs): return nil
                                                          Combine#337 ends at 7.941428ms
                                                      Gather#341 step 3/6 (+353ns): 211ns self time
                                                      Gather#341 step 4/6 (+564ns): scatter:
                                                        Task#334: pool=4
                                                        Task#334 step 1/2 (+0s): 6.021µs self time
                                                        Task#334 step 2/2 (+6.021µs): return nil
                                                        Task#334 ends at 543.219µs
                                                          Gather#334: index=1
                                                          Gather#334 step 1/2 (+0s): 998ns self time
                                                          Gather#334 step 2/2 (+998ns): return nil
                                                          Gather#334 ends at 544.217µs
                                                      Gather#341 step 5/6 (+564ns): 433ns self time
                                                      Gather#341 step 6/6 (+997ns): return nil
                                                      Gather#341 ends at 537.631µs
                                                  Combine#344 step 9/12 (+687ns): 157ns self time
                                                  Combine#344 step 10/12 (+844ns): scatter:
                                                    Task#339: pool=3
                                                    Task#339 step 1/2 (+0s): 9.996µs self time
                                                    Task#339 step 2/2 (+9.996µs): return nil
                                                    Task#339 ends at 536.853µs
                                                      Gather#339: index=1
                                                      Gather#339 step 1/2 (+0s): 984ns self time
                                                      Gather#339 step 2/2 (+984ns): return nil
                                                      Gather#339 ends at 537.837µs
                                                  Combine#344 step 11/12 (+844ns): 155ns self time
                                                  Combine#344 step 12/12 (+999ns): return nil
                                                  Combine#344 ends at 527.012µs
                                              Gather#345 step 3/4 (+499.988µs): 500.012µs self time
                                              Gather#345 step 4/4 (+1ms): return nil
                                              Gather#345 ends at 1.015032ms
                                          Gather#348 step 5/6 (+0s): 0s self time
                                          Gather#348 step 6/6 (+0s): return nil
                                          Gather#348 ends at 2.241µs
                                      Plan#15 step 5/6 (+0s): scatter:
                                        Task#330: pool=2
                                        Task#330 step 1/2 (+0s): 9.999µs self time
                                        Task#330 step 2/2 (+9.999µs): return nil
                                        Task#330 ends at 9.999µs
                                          Gather#330: index=2
                                          Gather#330 step 1/2 (+0s): 404ns self time
                                          Gather#330 step 2/2 (+404ns): return error
                                          Gather#330 ends at 10.403µs
                                      Plan#15 step 6/6 (+0s): ends at 7.941428ms
                                    Gather#328 step 3/4 (+7.942094ms): 133ns self time
                                    Gather#328 step 4/4 (+7.942227ms): return nil
                                    Gather#328 ends at 20.05321ms
                                Plan#13 step 10/12 (+0s): scatter:
                                  Task#326: pool=0
                                  Task#326 step 1/2 (+0s): 10.006µs self time
                                  Task#326 step 2/2 (+10.006µs): return nil
                                  Task#326 ends at 10.006µs
                                    Gather#326: index=7
                                    Gather#326 step 1/2 (+0s): 999ns self time
                                    Gather#326 step 2/2 (+999ns): return nil
                                    Gather#326 ends at 11.005µs
                                Plan#13 step 11/12 (+0s): scatter:
                                  Task#446: pool=0
                                  Task#446 step 1/2 (+0s): 5.962µs self time
                                  Task#446 step 2/2 (+5.962µs): return nil
                                  Task#446 ends at 5.962µs
                                    Gather#446: index=5
                                    Gather#446 step 1/2 (+0s): 1.005µs self time
                                    Gather#446 step 2/2 (+1.005µs): return nil
                                    Gather#446 ends at 6.967µs
                                Plan#13 step 12/12 (+0s): ends at 20.05321ms
                              Gather#287 step 3/6 (+20.053542ms): 600ns self time
                              Gather#287 step 4/6 (+20.054142ms): scatter:
                                Task#284: pool=0
                                Task#284 step 1/2 (+0s): 9.993µs self time
                                Task#284 step 2/2 (+9.993µs): return nil
                                Task#284 ends at 22.793325ms
                                  Gather#284: index=0
                                  Gather#284 step 1/2 (+0s): 1.011µs self time
                                  Gather#284 step 2/2 (+1.011µs): return nil
                                  Gather#284 ends at 22.794336ms
                              Gather#287 step 5/6 (+20.054142ms): 68ns self time
                              Gather#287 step 6/6 (+20.05421ms): return nil
                              Gather#287 ends at 22.7834ms
                          Gather#698 step 5/6 (+114.855µs): 57.405µs self time
                          Gather#698 step 6/6 (+172.26µs): return error
                          Gather#698 ends at 203.286µs
                      Gather#703 step 7/10 (+1.028µs): 4ns self time
                      Gather#703 step 8/10 (+1.032µs): scatter:
                        Task#546: pool=0
                        Task#546 step 1/2 (+0s): 9.997µs self time
                        Task#546 step 2/2 (+9.997µs): return nil
                        Task#546 ends at 31.032µs
                          Gather#546: index=2
                          Gather#546 step 1/6 (+0s): 343ns self time
                          Gather#546 step 2/6 (+343ns): subjob:
                            Plan#21: pathCount=13 taskCount=20 maxPathDuration=20.304723ms minGatherCount=18 maxGatherCount=28
                               TaskPools[0]: TaskPool#64: limit=3
                               TaskPools[1]: TaskPool#65: limit=1
                               TaskPools[2]: TaskPool#66: limit=4
                               CombinerPools[0]: CombinerPool#113: limit=9
                               CombinerPools[1]: CombinerPool#114: limit=1
                               Combiners[0]: pool=1
                               Combiners[1]: pool=0
                               Combiners[2]: pool=1
                               Combiners[3]: pool=0
                               Combiners[4]: pool=1
                               Combiners[5]: pool=0
                               Combiners[6]: pool=0
                               Combiners[7]: pool=1
                            Plan#21 step 1/6 (+0s): scatter:
                              Task#697: pool=2
                              Task#697 step 1/2 (+0s): 9.978µs self time
                              Task#697 step 2/2 (+9.978µs): return nil
                              Task#697 ends at 9.978µs
                                Gather#697: index=1
                                Gather#697 step 1/4 (+0s): 13ns self time
                                Gather#697 step 2/4 (+13ns): scatter:
                                  Task#695: pool=0
                                  Task#695 step 1/2 (+0s): 9.998µs self time
                                  Task#695 step 2/2 (+9.998µs): return nil
                                  Task#695 ends at 19.989µs
                                    Gather#695: index=2
                                    Gather#695 step 1/4 (+0s): 394ns self time
                                    Gather#695 step 2/4 (+394ns): scatter:
                                      Task#656: pool=0
                                      Task#656 step 1/2 (+0s): 7.073µs self time
                                      Task#656 step 2/2 (+7.073µs): return nil
                                      Task#656 ends at 27.456µs
                                        Gather#656: index=3
                                        Gather#656 step 1/2 (+0s): 1.707µs self time
                                        Gather#656 step 2/2 (+1.707µs): return nil
                                        Gather#656 ends at 29.163µs
                                    Gather#695 step 3/4 (+394ns): 402ns self time
                                    Gather#695 step 4/4 (+796ns): return error
                                    Gather#695 ends at 20.785µs
                                Gather#697 step 3/4 (+13ns): 40ns self time
                                Gather#697 step 4/4 (+53ns): return nil
                                Gather#697 ends at 10.031µs
                            Plan#21 step 2/6 (+0s): scatter:
                              Task#548: pool=2
                              Task#548 step 1/4 (+0s): 1.387µs self time
                              Task#548 step 2/4 (+1.387µs): subjob:
                                Plan#22: pathCount=17 taskCount=27 maxPathDuration=1.017615ms minGatherCount=16 maxGatherCount=34
                                   TaskPools[0]: TaskPool#67: limit=1
                                   TaskPools[1]: TaskPool#68: limit=2
                                   CombinerPools[0]: CombinerPool#115: limit=1
                                   CombinerPools[1]: CombinerPool#116: limit=2
                                   CombinerPools[2]: CombinerPool#117: limit=2
                                   CombinerPools[3]: CombinerPool#118: limit=2
                                   CombinerPools[4]: CombinerPool#119: limit=10
                                   Combiners[0]: pool=1
                                   Combiners[1]: pool=3
                                   Combiners[2]: pool=2
                                   Combiners[3]: pool=0
                                   Combiners[4]: pool=3
                                   Combiners[5]: pool=2
                                   Combiners[6]: pool=0
                                Plan#22 step 1/6 (+0s): scatter:
                                  Task#573: pool=1
                                  Task#573 step 1/2 (+0s): 9.935µs self time
                                  Task#573 step 2/2 (+9.935µs): return nil
                                  Task#573 ends at 9.935µs
                                    Gather#573: index=4
                                    Gather#573 step 1/12 (+0s): 268ns self time
                                    Gather#573 step 2/12 (+268ns): scatter:
                                      Task#571: pool=0
                                      Task#571 step 1/2 (+0s): 10.009µs self time
                                      Task#571 step 2/2 (+10.009µs): return nil
                                      Task#571 ends at 20.212µs
                                        Gather#571: index=3
                                        Gather#571 step 1/14 (+0s): 96.392µs self time
                                        Gather#571 step 2/14 (+96.392µs): scatter:
                                          Task#570: pool=0
                                          Task#570 step 1/2 (+0s): 10.092µs self time
                                          Task#570 step 2/2 (+10.092µs): return nil
                                          Task#570 ends at 126.696µs
                                            Combine#570: index=4 flush=<nil>
                                            Combine#570 step 1/4 (+0s): 750ns self time
                                            Combine#570 step 2/4 (+750ns): scatter:
                                              Task#558: pool=1
                                              Task#558 step 1/2 (+0s): 9.998µs self time
                                              Task#558 step 2/2 (+9.998µs): return nil
                                              Task#558 ends at 137.444µs
                                                Gather#558: index=0
                                                Gather#558 step 1/2 (+0s): 110.882µs self time
                                                Gather#558 step 2/2 (+110.882µs): return nil
                                                Gather#558 ends at 248.326µs
                                            Combine#570 step 3/4 (+750ns): 229ns self time
                                            Combine#570 step 4/4 (+979ns): return nil
                                            Combine#570 ends at 127.675µs
                                        Gather#571 step 3/14 (+96.392µs): 96.408µs self time
                                        Gather#571 step 4/14 (+192.8µs): scatter:
                                          Task#569: pool=1
                                          Task#569 step 1/2 (+0s): 10.017µs self time
                                          Task#569 step 2/2 (+10.017µs): return nil
                                          Task#569 ends at 223.029µs
                                            Gather#569: index=0
                                            Gather#569 step 1/4 (+0s): 541ns self time
                                            Gather#569 step 2/4 (+541ns): scatter:
                                              Task#563: pool=1
                                              Task#563 step 1/2 (+0s): 8.612µs self time
                                              Task#563 step 2/2 (+8.612µs): return nil
                                              Task#563 ends at 232.182µs
                                                Combine#563: index=1 flush=<nil>
                                                Combine#563 step 1/2 (+0s): 1.003µs self time
                                                Combine#563 step 2/2 (+1.003µs): return error
                                                Combine#563 ends at 233.185µs
                                            Gather#569 step 3/4 (+541ns): 506ns self time
                                            Gather#569 step 4/4 (+1.047µs): return nil
                                            Gather#569 ends at 224.076µs
                                        Gather#571 step 5/14 (+192.8µs): 92.993µs self time
                                        Gather#571 step 6/14 (+285.793µs): scatter:
                                          Task#567: pool=1
                                          Task#567 step 1/2 (+0s): 9.238µs self time
                                          Task#567 step 2/2 (+9.238µs): return nil
                                          Task#567 ends at 315.243µs
                                            Gather#567: index=2
                                            Gather#567 step 1/4 (+0s): 587ns self time
                                            Gather#567 step 2/4 (+587ns): scatter:
                                              Task#552: pool=0
                                              Task#552 step 1/2 (+0s): 9.998µs self time
                                              Task#552 step 2/2 (+9.998µs): return error
                                              Task#552 ends at 325.828µs
                                                Combine#552: index=3 flush=<nil>
                                                Combine#552 step 1/2 (+0s): 996ns self time
                                                Combine#552 step 2/2 (+996ns): return error
                                                Combine#552 ends at 326.824µs
                                            Gather#567 step 3/4 (+587ns): 411ns self time
                                            Gather#567 step 4/4 (+998ns): return nil
                                            Gather#567 ends at 316.241µs
                                        Gather#571 step 7/14 (+285.793µs): 97.201µs self time
                                        Gather#571 step 8/14 (+382.994µs): scatter:
                                          Task#559: pool=0
                                          Task#559 step 1/2 (+0s): 9.998µs self time
                                          Task#559 step 2/2 (+9.998µs): return nil
                                          Task#559 ends at 413.204µs
                                            Combine#559: index=3 flush=<nil>
                                            Combine#559 step 1/2 (+0s): 7.258µs self time
                                            Combine#559 step 2/2 (+7.258µs): return nil
                                            Combine#559 ends at 420.462µs
                                        Gather#571 step 9/14 (+382.994µs): 97.299µs self time
                                        Gather#571 step 10/14 (+480.293µs): scatter:
                                          Task#549: pool=1
                                          Task#549 step 1/2 (+0s): 10.003µs self time
                                          Task#549 step 2/2 (+10.003µs): return nil
                                          Task#549 ends at 510.508µs
                                            Gather#549: index=1
                                            Gather#549 step 1/2 (+0s): 843ns self time
                                            Gather#549 step 2/2 (+843ns): return nil
                                            Gather#549 ends at 511.351µs
                                        Gather#571 step 11/14 (+480.293µs): 97.253µs self time
                                        Gather#571 step 12/14 (+577.546µs): scatter:
                                          Task#551: pool=0
                                          Task#551 step 1/2 (+0s): 9.805µs self time
                                          Task#551 step 2/2 (+9.805µs): return nil
                                          Task#551 ends at 607.563µs
                                            Combine#551: index=3 flush=<nil>
                                            Combine#551 step 1/2 (+0s): 1.281µs self time
                                            Combine#551 step 2/2 (+1.281µs): return nil
                                            Combine#551 ends at 608.844µs
                                        Gather#571 step 13/14 (+577.546µs): 97.343µs self time
                                        Gather#571 step 14/14 (+674.889µs): return nil
                                        Gather#571 ends at 695.101µs
                                    Gather#573 step 3/12 (+268ns): 735ns self time
                                    Gather#573 step 4/12 (+1.003µs): scatter:
                                      Task#564: pool=0
                                      Task#564 step 1/2 (+0s): 9.597µs self time
                                      Task#564 step 2/2 (+9.597µs): return nil
                                      Task#564 ends at 20.535µs
                                        Combine#564: index=6 flush=<nil>
                                        Combine#564 step 1/2 (+0s): 996ns self time
                                        Combine#564 step 2/2 (+996ns): return nil
                                        Combine#564 ends at 21.531µs
                                    Gather#573 step 5/12 (+1.003µs): 1ns self time
                                    Gather#573 step 6/12 (+1.004µs): scatter:
                                      Task#556: pool=1
                                      Task#556 step 1/2 (+0s): 10.022µs self time
                                      Task#556 step 2/2 (+10.022µs): return nil
                                      Task#556 ends at 20.961µs
                                        Gather#556: index=2
                                        Gather#556 step 1/2 (+0s): 1.138µs self time
                                        Gather#556 step 2/2 (+1.138µs): return error
                                        Gather#556 ends at 22.099µs
                                    Gather#573 step 7/12 (+1.004µs): 0s self time
                                    Gather#573 step 8/12 (+1.004µs): scatter:
                                      Task#560: pool=1
                                      Task#560 step 1/2 (+0s): 9.989µs self time
                                      Task#560 step 2/2 (+9.989µs): return nil
                                      Task#560 ends at 20.928µs
                                        Gather#560: index=1
                                        Gather#560 step 1/2 (+0s): 415.068µs self time
                                        Gather#560 step 2/2 (+415.068µs): return nil
                                        Gather#560 ends at 435.996µs
                                    Gather#573 step 9/12 (+1.004µs): 4ns self time
                                    Gather#573 step 10/12 (+1.008µs): scatter:
                                      Task#565: pool=1
                                      Task#565 step 1/2 (+0s): 10µs self time
                                      Task#565 step 2/2 (+10µs): return nil
                                      Task#565 ends at 20.943µs
                                        Gather#565: index=3
                                        Gather#565 step 1/2 (+0s): 1.054µs self time
                                        Gather#565 step 2/2 (+1.054µs): return nil
                                        Gather#565 ends at 21.997µs
                                    Gather#573 step 11/12 (+1.008µs): 6ns self time
                                    Gather#573 step 12/12 (+1.014µs): return nil
                                    Gather#573 ends at 10.949µs
                                Plan#22 step 2/6 (+0s): scatter:
                                  Task#555: pool=1
                                  Task#555 step 1/2 (+0s): 9.998µs self time
                                  Task#555 step 2/2 (+9.998µs): return nil
                                  Task#555 ends at 9.998µs
                                    Gather#555: index=3
                                    Gather#555 step 1/2 (+0s): 529ns self time
                                    Gather#555 step 2/2 (+529ns): return nil
                                    Gather#555 ends at 10.527µs
                                Plan#22 step 3/6 (+0s): scatter:
                                  Task#574: pool=1
                                  Task#574 step 1/2 (+0s): 20.824µs self time
                                  Task#574 step 2/2 (+20.824µs): return nil
                                  Task#574 ends at 20.824µs
                                    Gather#574: index=1
                                    Gather#574 step 1/4 (+0s): 4ns self time
                                    Gather#574 step 2/4 (+4ns): scatter:
                                      Task#572: pool=1
                                      Task#572 step 1/2 (+0s): 9.998µs self time
                                      Task#572 step 2/2 (+9.998µs): return nil
                                      Task#572 ends at 30.826µs
                                        Gather#572: index=4
                                        Gather#572 step 1/6 (+0s): 333ns self time
                                        Gather#572 step 2/6 (+333ns): scatter:
                                          Task#554: pool=1
                                          Task#554 step 1/2 (+0s): 8.877µs self time
                                          Task#554 step 2/2 (+8.877µs): return nil
                                          Task#554 ends at 40.036µs
                                            Combine#554: index=5 flush=<nil>
                                            Combine#554 step 1/2 (+0s): 504ns self time
                                            Combine#554 step 2/2 (+504ns): return nil
                                            Combine#554 ends at 40.54µs
                                        Gather#572 step 3/6 (+333ns): 321ns self time
                                        Gather#572 step 4/6 (+654ns): scatter:
                                          Task#568: pool=0
                                          Task#568 step 1/2 (+0s): 10.001µs self time
                                          Task#568 step 2/2 (+10.001µs): return nil
                                          Task#568 ends at 41.481µs
                                            Gather#568: index=4
                                            Gather#568 step 1/8 (+0s): 205ns self time
                                            Gather#568 step 2/8 (+205ns): scatter:
                                              Task#566: pool=0
                                              Task#566 step 1/2 (+0s): 10µs self time
                                              Task#566 step 2/2 (+10µs): return nil
                                              Task#566 ends at 51.686µs
                                                Gather#566: index=1
                                                Gather#566 step 1/4 (+0s): 1.14µs self time
                                                Gather#566 step 2/4 (+1.14µs): scatter:
                                                  Task#550: pool=1
                                                  Task#550 step 1/2 (+0s): 9.997µs self time
                                                  Task#550 step 2/2 (+9.997µs): return nil
                                                  Task#550 ends at 62.823µs
                                                    Combine#550: index=1 flush=<nil>
                                                    Combine#550 step 1/2 (+0s): 1.001µs self time
                                                    Combine#550 step 2/2 (+1.001µs): return nil
                                                    Combine#550 ends at 63.824µs
                                                Gather#566 step 3/4 (+1.14µs): 103ns self time
                                                Gather#566 step 4/4 (+1.243µs): return nil
                                                Gather#566 ends at 52.929µs
                                            Gather#568 step 3/8 (+205ns): 265ns self time
                                            Gather#568 step 4/8 (+470ns): scatter:
                                              Task#557: pool=0
                                              Task#557 step 1/2 (+0s): 9.997µs self time
                                              Task#557 step 2/2 (+9.997µs): return nil
                                              Task#557 ends at 51.948µs
                                                Combine#557: index=5 flush=<nil>
                                                Combine#557 step 1/2 (+0s): 1.001µs self time
                                                Combine#557 step 2/2 (+1.001µs): return nil
                                                Combine#557 ends at 52.949µs
                                            Gather#568 step 5/8 (+470ns): 29ns self time
                                            Gather#568 step 6/8 (+499ns): scatter:
                                              Task#553: pool=0
                                              Task#553 step 1/2 (+0s): 8.796µs self time
                                              Task#553 step 2/2 (+8.796µs): return nil
                                              Task#553 ends at 50.776µs
                                                Gather#553: index=0
                                                Gather#553 step 1/2 (+0s): 955ns self time
                                                Gather#553 step 2/2 (+955ns): return nil
                                                Gather#553 ends at 51.731µs
                                            Gather#568 step 7/8 (+499ns): 506ns self time
                                            Gather#568 step 8/8 (+1.005µs): return nil
                                            Gather#568 ends at 42.486µs
                                        Gather#572 step 5/6 (+654ns): 352ns self time
                                        Gather#572 step 6/6 (+1.006µs): return nil
                                        Gather#572 ends at 31.832µs
                                    Gather#574 step 3/4 (+4ns): 6ns self time
                                    Gather#574 step 4/4 (+10ns): return nil
                                    Gather#574 ends at 20.834µs
                                Plan#22 step 4/6 (+0s): scatter:
                                  Task#562: pool=1
                                  Task#562 step 1/2 (+0s): 17.615µs self time
                                  Task#562 step 2/2 (+17.615µs): return nil
                                  Task#562 ends at 17.615µs
                                    Gather#562: index=1
                                    Gather#562 step 1/2 (+0s): 1ms self time
                                    Gather#562 step 2/2 (+1ms): return nil
                                    Gather#562 ends at 1.017615ms
                                Plan#22 step 5/6 (+0s): scatter:
                                  Task#575: pool=0
                                  Task#575 step 1/2 (+0s): 10µs self time
                                  Task#575 step 2/2 (+10µs): return nil
                                  Task#575 ends at 10µs
                                    Combine#575: index=2 flush=<nil>
                                    Combine#575 step 1/4 (+0s): 159ns self time
                                    Combine#575 step 2/4 (+159ns): scatter:
                                      Task#561: pool=0
                                      Task#561 step 1/2 (+0s): 9.999µs self time
                                      Task#561 step 2/2 (+9.999µs): return nil
                                      Task#561 ends at 20.158µs
                                        Combine#561: index=0 flush=<nil>
                                        Combine#561 step 1/2 (+0s): 995ns self time
                                        Combine#561 step 2/2 (+995ns): return nil
                                        Combine#561 ends at 21.153µs
                                    Combine#575 step 3/4 (+159ns): 367ns self time
                                    Combine#575 step 4/4 (+526ns): return nil
                                    Combine#575 ends at 10.526µs
                                Plan#22 step 6/6 (+0s): ends at 1.017615ms
                              Task#548 step 3/4 (+1.019002ms): 1.392µs self time
                              Task#548 step 4/4 (+1.020394ms): return nil
                              Task#548 ends at 1.020394ms
                                Combine#548: index=0 flush=<nil>
                                Combine#548 step 1/2 (+0s): 788ns self time
                                Combine#548 step 2/2 (+788ns): return nil
                                Combine#548 ends at 1.021182ms
                            Plan#21 step 3/6 (+0s): scatter:
                              Task#696: pool=1
                              Task#696 step 1/2 (+0s): 10.001µs self time
                              Task#696 step 2/2 (+10.001µs): return nil
                              Task#696 ends at 10.001µs
                                Gather#696: index=5
                                Gather#696 step 1/4 (+0s): 436ns self time
                                Gather#696 step 2/4 (+436ns): scatter:
                                  Task#694: pool=1
                                  Task#694 step 1/2 (+0s): 9.97µs self time
                                  Task#694 step 2/2 (+9.97µs): return nil
                                  Task#694 ends at 20.407µs
                                    Gather#694: index=4
                                    Gather#694 step 1/4 (+0s): 26.591µs self time
                                    Gather#694 step 2/4 (+26.591µs): scatter:
                                      Task#661: pool=0
                                      Task#661 step 1/2 (+0s): 9.997µs self time
                                      Task#661 step 2/2 (+9.997µs): return nil
                                      Task#661 ends at 56.995µs
                                        Gather#661: index=6
                                        Gather#661 step 1/16 (+0s): 124ns self time
                                        Gather#661 step 2/16 (+124ns): scatter:
                                          Task#658: pool=0
                                          Task#658 step 1/2 (+0s): 16.497µs self time
                                          Task#658 step 2/2 (+16.497µs): return nil
                                          Task#658 ends at 73.616µs
                                            Gather#658: index=4
                                            Gather#658 step 1/2 (+0s): 1µs self time
                                            Gather#658 step 2/2 (+1µs): return nil
                                            Gather#658 ends at 74.616µs
                                        Gather#661 step 3/16 (+124ns): 15ns self time
                                        Gather#661 step 4/16 (+139ns): scatter:
                                          Task#659: pool=2
                                          Task#659 step 1/2 (+0s): 10ms self time
                                          Task#659 step 2/2 (+10ms): return nil
                                          Task#659 ends at 10.057134ms
                                            Gather#659: index=1
                                            Gather#659 step 1/4 (+0s): 0s self time
                                            Gather#659 step 2/4 (+0s): scatter:
                                              Task#576: pool=1
                                              Task#576 step 1/4 (+0s): 4.991µs self time
                                              Task#576 step 2/4 (+4.991µs): subjob:
                                                Plan#23: pathCount=27 taskCount=39 maxPathDuration=10.236591ms minGatherCount=31 maxGatherCount=72
                                                   TaskPools[0]: TaskPool#69: limit=4
                                                   TaskPools[1]: TaskPool#70: limit=8
                                                   CombinerPools[0]: CombinerPool#120: limit=10
                                                   CombinerPools[1]: CombinerPool#121: limit=7
                                                   CombinerPools[2]: CombinerPool#122: limit=5
                                                   CombinerPools[3]: CombinerPool#123: limit=10
                                                   CombinerPools[4]: CombinerPool#124: limit=2
                                                   Combiners[0]: pool=4
                                                   Combiners[1]: pool=2
                                                   Combiners[2]: pool=4
                                                   Combiners[3]: pool=0
                                                Plan#23 step 1/15 (+0s): scatter:
                                                  Task#596: pool=0
                                                  Task#596 step 1/2 (+0s): 546.541µs self time
                                                  Task#596 step 2/2 (+546.541µs): return nil
                                                  Task#596 ends at 546.541µs
                                                    Gather#596: index=0
                                                    Gather#596 step 1/2 (+0s): 77ns self time
                                                    Gather#596 step 2/2 (+77ns): return error
                                                    Gather#596 ends at 546.618µs
                                                Plan#23 step 2/15 (+0s): scatter:
                                                  Task#615: pool=1
                                                  Task#615 step 1/2 (+0s): 9.998µs self time
                                                  Task#615 step 2/2 (+9.998µs): return nil
                                                  Task#615 ends at 9.998µs
                                                    Gather#615: index=13
                                                    Gather#615 step 1/10 (+0s): 196ns self time
                                                    Gather#615 step 2/10 (+196ns): scatter:
                                                      Task#610: pool=1
                                                      Task#610 step 1/2 (+0s): 13.445µs self time
                                                      Task#610 step 2/2 (+13.445µs): return nil
                                                      Task#610 ends at 23.639µs
                                                        Gather#610: index=13
                                                        Gather#610 step 1/4 (+0s): 1.921µs self time
                                                        Gather#610 step 2/4 (+1.921µs): scatter:
                                                          Task#608: pool=0
                                                          Task#608 step 1/2 (+0s): 34.055µs self time
                                                          Task#608 step 2/2 (+34.055µs): return nil
                                                          Task#608 ends at 59.615µs
                                                            Gather#608: index=11
                                                            Gather#608 step 1/4 (+0s): 212.384µs self time
                                                            Gather#608 step 2/4 (+212.384µs): scatter:
                                                              Task#606: pool=0
                                                              Task#606 step 1/2 (+0s): 8.199µs self time
                                                              Task#606 step 2/2 (+8.199µs): return nil
                                                              Task#606 ends at 280.198µs
                                                                Combine#606: index=3 flush=<nil>
                                                                Combine#606 step 1/4 (+0s): 233.336µs self time
                                                                Combine#606 step 2/4 (+233.336µs): scatter:
                                                                  Task#588: pool=1
                                                                  Task#588 step 1/2 (+0s): 10.41µs self time
                                                                  Task#588 step 2/2 (+10.41µs): return nil
                                                                  Task#588 ends at 523.944µs
                                                                    Gather#588: index=10
                                                                    Gather#588 step 1/2 (+0s): 1µs self time
                                                                    Gather#588 step 2/2 (+1µs): return nil
                                                                    Gather#588 ends at 524.944µs
                                                                Combine#606 step 3/4 (+233.336µs): 195.412µs self time
                                                                Combine#606 step 4/4 (+428.748µs): return nil
                                                                Combine#606 ends at 708.946µs
                                                            Gather#608 step 3/4 (+212.384µs): 114.605µs self time
                                                            Gather#608 step 4/4 (+326.989µs): return nil
                                                            Gather#608 ends at 386.604µs
                                                        Gather#610 step 3/4 (+1.921µs): 1.896µs self time
                                                        Gather#610 step 4/4 (+3.817µs): return nil
                                                        Gather#610 ends at 27.456µs
                                                    Gather#615 step 3/10 (+196ns): 549ns self time
                                                    Gather#615 step 4/10 (+745ns): scatter:
                                                      Task#591: pool=1
                                                      Task#591 step 1/2 (+0s): 10µs self time
                                                      Task#591 step 2/2 (+10µs): return nil
                                                      Task#591 ends at 20.743µs
                                                        Combine#591: index=2 flush=<nil>
                                                        Combine#591 step 1/2 (+0s): 0s self time
                                                        Combine#591 step 2/2 (+0s): return nil
                                                        Combine#591 ends at 20.743µs
                                                    Gather#615 step 5/10 (+745ns): 73ns self time
                                                    Gather#615 step 6/10 (+818ns): scatter:
                                                      Task#583: pool=1
                                                      Task#583 step 1/2 (+0s): 10.002µs self time
                                                      Task#583 step 2/2 (+10.002µs): return nil
                                                      Task#583 ends at 20.818µs
                                                        Gather#583: index=1
                                                        Gather#583 step 1/2 (+0s): 998ns self time
                                                        Gather#583 step 2/2 (+998ns): return nil
                                                        Gather#583 ends at 21.816µs
                                                    Gather#615 step 7/10 (+818ns): 80ns self time
                                                    Gather#615 step 8/10 (+898ns): scatter:
                                                      Task#590: pool=1
                                                      Task#590 step 1/2 (+0s): 9.979µs self time
                                                      Task#590 step 2/2 (+9.979µs): return nil
                                                      Task#590 ends at 20.875µs
                                                        Gather#590: index=1
                                                        Gather#590 step 1/2 (+0s): 1.001µs self time
                                                        Gather#590 step 2/2 (+1.001µs): return nil
                                                        Gather#590 ends at 21.876µs
                                                    Gather#615 step 9/10 (+898ns): 85ns self time
                                                    Gather#615 step 10/10 (+983ns): return nil
                                                    Gather#615 ends at 10.981µs
                                                Plan#23 step 3/15 (+0s): scatter:
                                                  Task#581: pool=0
                                                  Task#581 step 1/2 (+0s): 6.575µs self time
                                                  Task#581 step 2/2 (+6.575µs): return nil
                                                  Task#581 ends at 6.575µs
                                                    Gather#581: index=7
                                                    Gather#581 step 1/2 (+0s): 1.001µs self time
                                                    Gather#581 step 2/2 (+1.001µs): return nil
                                                    Gather#581 ends at 7.576µs
                                                Plan#23 step 4/15 (+0s): scatter:
                                                  Task#599: pool=1
                                                  Task#599 step 1/2 (+0s): 10.003µs self time
                                                  Task#599 step 2/2 (+10.003µs): return nil
                                                  Task#599 ends at 10.003µs
                                                    Combine#599: index=2 flush=<nil>
                                                    Combine#599 step 1/2 (+0s): 1µs self time
                                                    Combine#599 step 2/2 (+1µs): return nil
                                                    Combine#599 ends at 11.003µs
                                                Plan#23 step 5/15 (+0s): scatter:
                                                  Task#614: pool=1
                                                  Task#614 step 1/2 (+0s): 10ms self time
                                                  Task#614 step 2/2 (+10ms): return nil
                                                  Task#614 ends at 10ms
                                                    Combine#614: index=1 flush=<nil>
                                                    Combine#614 step 1/4 (+0s): 506ns self time
                                                    Combine#614 step 2/4 (+506ns): scatter:
                                                      Task#601: pool=1
                                                      Task#601 step 1/2 (+0s): 20.801µs self time
                                                      Task#601 step 2/2 (+20.801µs): return nil
                                                      Task#601 ends at 10.021307ms
                                                        Gather#601: index=13
                                                        Gather#601 step 1/2 (+0s): 1.029µs self time
                                                        Gather#601 step 2/2 (+1.029µs): return nil
                                                        Gather#601 ends at 10.022336ms
                                                    Combine#614 step 3/4 (+506ns): 506ns self time
                                                    Combine#614 step 4/4 (+1.012µs): return nil
                                                    Combine#614 ends at 10.001012ms
                                                Plan#23 step 6/15 (+0s): scatter:
                                                  Task#613: pool=1
                                                  Task#613 step 1/2 (+0s): 10.011µs self time
                                                  Task#613 step 2/2 (+10.011µs): return nil
                                                  Task#613 ends at 10.011µs
                                                    Gather#613: index=3
                                                    Gather#613 step 1/8 (+0s): 256ns self time
                                                    Gather#613 step 2/8 (+256ns): scatter:
                                                      Task#611: pool=1
                                                      Task#611 step 1/2 (+0s): 9.999µs self time
                                                      Task#611 step 2/2 (+9.999µs): return nil
                                                      Task#611 ends at 20.266µs
                                                        Gather#611: index=1
                                                        Gather#611 step 1/10 (+0s): 607ns self time
                                                        Gather#611 step 2/10 (+607ns): scatter:
                                                          Task#592: pool=0
                                                          Task#592 step 1/2 (+0s): 10.212µs self time
                                                          Task#592 step 2/2 (+10.212µs): return nil
                                                          Task#592 ends at 31.085µs
                                                            Gather#592: index=6
                                                            Gather#592 step 1/2 (+0s): 0s self time
                                                            Gather#592 step 2/2 (+0s): return nil
                                                            Gather#592 ends at 31.085µs
                                                        Gather#611 step 3/10 (+607ns): 1.327µs self time
                                                        Gather#611 step 4/10 (+1.934µs): scatter:
                                                          Task#609: pool=0
                                                          Task#609 step 1/2 (+0s): 10.028µs self time
                                                          Task#609 step 2/2 (+10.028µs): return nil
                                                          Task#609 ends at 32.228µs
                                                            Gather#609: index=14
                                                            Gather#609 step 1/10 (+0s): 129.686µs self time
                                                            Gather#609 step 2/10 (+129.686µs): scatter:
                                                              Task#604: pool=1
                                                              Task#604 step 1/2 (+0s): 10ms self time
                                                              Task#604 step 2/2 (+10ms): return nil
                                                              Task#604 ends at 10.161914ms
                                                                Gather#604: index=12
                                                                Gather#604 step 1/6 (+0s): 331ns self time
                                                                Gather#604 step 2/6 (+331ns): scatter:
                                                                  Task#598: pool=0
                                                                  Task#598 step 1/2 (+0s): 9.995µs self time
                                                                  Task#598 step 2/2 (+9.995µs): return nil
                                                                  Task#598 ends at 10.17224ms
                                                                    Combine#598: index=2 flush=<nil>
                                                                    Combine#598 step 1/2 (+0s): 64.351µs self time
                                                                    Combine#598 step 2/2 (+64.351µs): return nil
                                                                    Combine#598 ends at 10.236591ms
                                                                Gather#604 step 3/6 (+331ns): 49ns self time
                                                                Gather#604 step 4/6 (+380ns): scatter:
                                                                  Task#577: pool=1
                                                                  Task#577 step 1/2 (+0s): 10µs self time
                                                                  Task#577 step 2/2 (+10µs): return nil
                                                                  Task#577 ends at 10.172294ms
                                                                    Gather#577: index=11
                                                                    Gather#577 step 1/2 (+0s): 1µs self time
                                                                    Gather#577 step 2/2 (+1µs): return nil
                                                                    Gather#577 ends at 10.173294ms
                                                                Gather#604 step 5/6 (+380ns): 605ns self time
                                                                Gather#604 step 6/6 (+985ns): return nil
                                                                Gather#604 ends at 10.162899ms
                                                            Gather#609 step 3/10 (+129.686µs): 129.655µs self time
                                                            Gather#609 step 4/10 (+259.341µs): scatter:
                                                              Task#605: pool=0
                                                              Task#605 step 1/2 (+0s): 10.002µs self time
                                                              Task#605 step 2/2 (+10.002µs): return nil
                                                              Task#605 ends at 301.571µs
                                                                Gather#605: index=4
                                                                Gather#605 step 1/4 (+0s): 499ns self time
                                                                Gather#605 step 2/4 (+499ns): scatter:
                                                                  Task#589: pool=0
                                                                  Task#589 step 1/2 (+0s): 10.01µs self time
                                                                  Task#589 step 2/2 (+10.01µs): return nil
                                                                  Task#589 ends at 312.08µs
                                                                    Gather#589: index=6
                                                                    Gather#589 step 1/2 (+0s): 38.211µs self time
                                                                    Gather#589 step 2/2 (+38.211µs): return nil
                                                                    Gather#589 ends at 350.291µs
                                                                Gather#605 step 3/4 (+499ns): 500ns self time
                                                                Gather#605 step 4/4 (+999ns): return nil
                                                                Gather#605 ends at 302.57µs
                                                            Gather#609 step 5/10 (+259.341µs): 129.77µs self time
                                                            Gather#609 step 6/10 (+389.111µs): scatter:
                                                              Task#579: pool=0
                                                              Task#579 step 1/2 (+0s): 10.166µs self time
                                                              Task#579 step 2/2 (+10.166µs): return error
                                                              Task#579 ends at 431.505µs
                                                                Gather#579: index=3
                                                                Gather#579 step 1/2 (+0s): 622ns self time
                                                                Gather#579 step 2/2 (+622ns): return nil
                                                                Gather#579 ends at 432.127µs
                                                            Gather#609 step 7/10 (+389.111µs): 129.608µs self time
                                                            Gather#609 step 8/10 (+518.719µs): scatter:
                                                              Task#585: pool=0
                                                              Task#585 step 1/2 (+0s): 9.996µs self time
                                                              Task#585 step 2/2 (+9.996µs): return nil
                                                              Task#585 ends at 560.943µs
                                                                Gather#585: index=11
                                                                Gather#585 step 1/2 (+0s): 1.08µs self time
                                                                Gather#585 step 2/2 (+1.08µs): return nil
                                                                Gather#585 ends at 562.023µs
                                                            Gather#609 step 9/10 (+518.719µs): 129.682µs self time
                                                            Gather#609 step 10/10 (+648.401µs): return nil
                                                            Gather#609 ends at 680.629µs
                                                        Gather#611 step 5/10 (+1.934µs): 288ns self time
                                                        Gather#611 step 6/10 (+2.222µs): scatter:
                                                          Task#584: pool=0
                                                          Task#584 step 1/2 (+0s): 10.001µs self time
                                                          Task#584 step 2/2 (+10.001µs): return nil
                                                          Task#584 ends at 32.489µs
                                                            Combine#584: index=1 flush=<nil>
                                                            Combine#584 step 1/2 (+0s): 1µs self time
                                                            Combine#584 step 2/2 (+1µs): return nil
                                                            Combine#584 ends at 33.489µs
                                                        Gather#611 step 7/10 (+2.222µs): 275ns self time
                                                        Gather#611 step 8/10 (+2.497µs): scatter:
                                                          Task#607: pool=0
                                                          Task#607 step 1/2 (+0s): 10.019µs self time
                                                          Task#607 step 2/2 (+10.019µs): return nil
                                                          Task#607 ends at 32.782µs
                                                            Gather#607: index=11
                                                            Gather#607 step 1/6 (+0s): 144ns self time
                                                            Gather#607 step 2/6 (+144ns): scatter:
                                                              Task#600: pool=1
                                                              Task#600 step 1/2 (+0s): 9.935µs self time
                                                              Task#600 step 2/2 (+9.935µs): return nil
                                                              Task#600 ends at 42.861µs
                                                                Gather#600: index=10
                                                                Gather#600 step 1/2 (+0s): 2.775µs self time
                                                                Gather#600 step 2/2 (+2.775µs): return nil
                                                                Gather#600 ends at 45.636µs
                                                            Gather#607 step 3/6 (+144ns): 616ns self time
                                                            Gather#607 step 4/6 (+760ns): scatter:
                                                              Task#602: pool=1
                                                              Task#602 step 1/2 (+0s): 9.976µs self time
                                                              Task#602 step 2/2 (+9.976µs): return nil
                                                              Task#602 ends at 43.518µs
                                                                Gather#602: index=4
                                                                Gather#602 step 1/2 (+0s): 1.024µs self time
                                                                Gather#602 step 2/2 (+1.024µs): return nil
                                                                Gather#602 ends at 44.542µs
                                                            Gather#607 step 5/6 (+760ns): 619ns self time
                                                            Gather#607 step 6/6 (+1.379µs): return nil
                                                            Gather#607 ends at 34.161µs
                                                        Gather#611 step 9/10 (+2.497µs): 273ns self time
                                                        Gather#611 step 10/10 (+2.77µs): return nil
                                                        Gather#611 ends at 23.036µs
                                                    Gather#613 step 3/8 (+256ns): 0s self time
                                                    Gather#613 step 4/8 (+256ns): scatter:
                                                      Task#597: pool=1
                                                      Task#597 step 1/2 (+0s): 9.998µs self time
                                                      Task#597 step 2/2 (+9.998µs): return error
                                                      Task#597 ends at 20.265µs
                                                        Gather#597: index=5
                                                        Gather#597 step 1/2 (+0s): 999ns self time
                                                        Gather#597 step 2/2 (+999ns): return nil
                                                        Gather#597 ends at 21.264µs
                                                    Gather#613 step 5/8 (+256ns): 276ns self time
                                                    Gather#613 step 6/8 (+532ns): scatter:
                                                      Task#593: pool=0
                                                      Task#593 step 1/2 (+0s): 10.957µs self time
                                                      Task#593 step 2/2 (+10.957µs): return nil
                                                      Task#593 ends at 21.5µs
                                                        Gather#593: index=5
                                                        Gather#593 step 1/2 (+0s): 849ns self time
                                                        Gather#593 step 2/2 (+849ns): return nil
                                                        Gather#593 ends at 22.349µs
                                                    Gather#613 step 7/8 (+532ns): 470ns self time
                                                    Gather#613 step 8/8 (+1.002µs): return nil
                                                    Gather#613 ends at 11.013µs
                                                Plan#23 step 7/15 (+0s): scatter:
                                                  Task#578: pool=0
                                                  Task#578 step 1/2 (+0s): 4.911278ms self time
                                                  Task#578 step 2/2 (+4.911278ms): return error
                                                  Task#578 ends at 4.911278ms
                                                    Combine#578: index=1 flush=<nil>
                                                    Combine#578 step 1/2 (+0s): 654ns self time
                                                    Combine#578 step 2/2 (+654ns): return nil
                                                    Combine#578 ends at 4.911932ms
                                                Plan#23 step 8/15 (+0s): scatter:
                                                  Task#595: pool=0
                                                  Task#595 step 1/2 (+0s): 5.712µs self time
                                                  Task#595 step 2/2 (+5.712µs): return nil
                                                  Task#595 ends at 5.712µs
                                                    Gather#595: index=6
                                                    Gather#595 step 1/2 (+0s): 999ns self time
                                                    Gather#595 step 2/2 (+999ns): return nil
                                                    Gather#595 ends at 6.711µs
                                                Plan#23 step 9/15 (+0s): scatter:
                                                  Task#580: pool=1
                                                  Task#580 step 1/2 (+0s): 9.376µs self time
                                                  Task#580 step 2/2 (+9.376µs): return nil
                                                  Task#580 ends at 9.376µs
                                                    Gather#580: index=3
                                                    Gather#580 step 1/2 (+0s): 239ns self time
                                                    Gather#580 step 2/2 (+239ns): return nil
                                                    Gather#580 ends at 9.615µs
                                                Plan#23 step 10/15 (+0s): scatter:
                                                  Task#612: pool=1
                                                  Task#612 step 1/2 (+0s): 9.998µs self time
                                                  Task#612 step 2/2 (+9.998µs): return nil
                                                  Task#612 ends at 9.998µs
                                                    Gather#612: index=7
                                                    Gather#612 step 1/4 (+0s): 182ns self time
                                                    Gather#612 step 2/4 (+182ns): scatter:
                                                      Task#603: pool=1
                                                      Task#603 step 1/2 (+0s): 10.001µs self time
                                                      Task#603 step 2/2 (+10.001µs): return nil
                                                      Task#603 ends at 20.181µs
                                                        Gather#603: index=9
                                                        Gather#603 step 1/2 (+0s): 1ms self time
                                                        Gather#603 step 2/2 (+1ms): return nil
                                                        Gather#603 ends at 1.020181ms
                                                    Gather#612 step 3/4 (+182ns): 47ns self time
                                                    Gather#612 step 4/4 (+229ns): return nil
                                                    Gather#612 ends at 10.227µs
                                                Plan#23 step 11/15 (+0s): scatter:
                                                  Task#582: pool=0
                                                  Task#582 step 1/2 (+0s): 10µs self time
                                                  Task#582 step 2/2 (+10µs): return nil
                                                  Task#582 ends at 10µs
                                                    Gather#582: index=1
                                                    Gather#582 step 1/2 (+0s): 1.003µs self time
                                                    Gather#582 step 2/2 (+1.003µs): return nil
                                                    Gather#582 ends at 11.003µs
                                                Plan#23 step 12/15 (+0s): scatter:
                                                  Task#587: pool=0
                                                  Task#587 step 1/2 (+0s): 10.001µs self time
                                                  Task#587 step 2/2 (+10.001µs): return nil
                                                  Task#587 ends at 10.001µs
                                                    Combine#587: index=3 flush=<nil>
                                                    Combine#587 step 1/2 (+0s): 951ns self time
                                                    Combine#587 step 2/2 (+951ns): return nil
                                                    Combine#587 ends at 10.952µs
                                                Plan#23 step 13/15 (+0s): scatter:
                                                  Task#594: pool=1
                                                  Task#594 step 1/2 (+0s): 12.232µs self time
                                                  Task#594 step 2/2 (+12.232µs): return nil
                                                  Task#594 ends at 12.232µs
                                                    Gather#594: index=4
                                                    Gather#594 step 1/2 (+0s): 998ns self time
                                                    Gather#594 step 2/2 (+998ns): return nil
                                                    Gather#594 ends at 13.23µs
                                                Plan#23 step 14/15 (+0s): scatter:
                                                  Task#586: pool=0
                                                  Task#586 step 1/2 (+0s): 9.757µs self time
                                                  Task#586 step 2/2 (+9.757µs): return nil
                                                  Task#586 ends at 9.757µs
                                                    Gather#586: index=14
                                                    Gather#586 step 1/2 (+0s): 1µs self time
                                                    Gather#586 step 2/2 (+1µs): return nil
                                                    Gather#586 ends at 10.757µs
                                                Plan#23 step 15/15 (+0s): ends at 10.236591ms
                                              Task#576 step 3/4 (+10.241582ms): 5.008µs self time
                                              Task#576 step 4/4 (+10.24659ms): return nil
                                              Task#576 ends at 20.303724ms
                                                Gather#576: index=1
                                                Gather#576 step 1/2 (+0s): 999ns self time
                                                Gather#576 step 2/2 (+999ns): return nil
                                                Gather#576 ends at 20.304723ms
                                            Gather#659 step 3/4 (+0s): 0s self time
                                            Gather#659 step 4/4 (+0s): return nil
                                            Gather#659 ends at 10.057134ms
                                        Gather#661 step 5/16 (+139ns): 93ns self time
                                        Gather#661 step 6/16 (+232ns): subjob:
                                          Plan#25: pathCount=20 taskCount=32 maxPathDuration=9.41487ms minGatherCount=22 maxGatherCount=32
                                             TaskPools[0]: TaskPool#72: limit=1
                                             TaskPools[1]: TaskPool#73: limit=6
                                             CombinerPools[0]: CombinerPool#126: limit=10
                                             CombinerPools[1]: CombinerPool#127: limit=2
                                             CombinerPools[2]: CombinerPool#128: limit=1
                                             Combiners[0]: pool=2
                                          Plan#25 step 1/13 (+0s): scatter:
                                            Task#668: pool=1
                                            Task#668 step 1/2 (+0s): 3.452844ms self time
                                            Task#668 step 2/2 (+3.452844ms): return nil
                                            Task#668 ends at 3.452844ms
                                              Gather#668: index=1
                                              Gather#668 step 1/2 (+0s): 996ns self time
                                              Gather#668 step 2/2 (+996ns): return nil
                                              Gather#668 ends at 3.45384ms
                                          Plan#25 step 2/13 (+0s): scatter:
                                            Task#679: pool=0
                                            Task#679 step 1/2 (+0s): 10.007µs self time
                                            Task#679 step 2/2 (+10.007µs): return nil
                                            Task#679 ends at 10.007µs
                                              Gather#679: index=0
                                              Gather#679 step 1/2 (+0s): 999ns self time
                                              Gather#679 step 2/2 (+999ns): return nil
                                              Gather#679 ends at 11.006µs
                                          Plan#25 step 3/13 (+0s): scatter:
                                            Task#692: pool=1
                                            Task#692 step 1/2 (+0s): 4.662µs self time
                                            Task#692 step 2/2 (+4.662µs): return nil
                                            Task#692 ends at 4.662µs
                                              Combine#692: index=0 flush=<nil>
                                              Combine#692 step 1/4 (+0s): 38ns self time
                                              Combine#692 step 2/4 (+38ns): scatter:
                                                Task#665: pool=1
                                                Task#665 step 1/2 (+0s): 9.984µs self time
                                                Task#665 step 2/2 (+9.984µs): return nil
                                                Task#665 ends at 14.684µs
                                                  Gather#665: index=1
                                                  Gather#665 step 1/2 (+0s): 256ns self time
                                                  Gather#665 step 2/2 (+256ns): return nil
                                                  Gather#665 ends at 14.94µs
                                              Combine#692 step 3/4 (+38ns): 362ns self time
                                              Combine#692 step 4/4 (+400ns): return nil
                                              Combine#692 ends at 5.062µs
                                          Plan#25 step 4/13 (+0s): scatter:
                                            Task#690: pool=0
                                            Task#690 step 1/2 (+0s): 6.194452ms self time
                                            Task#690 step 2/2 (+6.194452ms): return nil
                                            Task#690 ends at 6.194452ms
                                              Gather#690: index=1
                                              Gather#690 step 1/6 (+0s): 180ns self time
                                              Gather#690 step 2/6 (+180ns): scatter:
                                                Task#678: pool=0
                                                Task#678 step 1/2 (+0s): 9.907µs self time
                                                Task#678 step 2/2 (+9.907µs): return nil
                                                Task#678 ends at 6.204539ms
                                                  Combine#678: index=0 flush=<nil>
                                                  Combine#678 step 1/2 (+0s): 962.4µs self time
                                                  Combine#678 step 2/2 (+962.4µs): return nil
                                                  Combine#678 ends at 7.166939ms
                                              Gather#690 step 3/6 (+180ns): 181ns self time
                                              Gather#690 step 4/6 (+361ns): scatter:
                                                Task#672: pool=1
                                                Task#672 step 1/2 (+0s): 1.022µs self time
                                                Task#672 step 2/2 (+1.022µs): return nil
                                                Task#672 ends at 6.195835ms
                                                  Gather#672: index=1
                                                  Gather#672 step 1/2 (+0s): 1.007µs self time
                                                  Gather#672 step 2/2 (+1.007µs): return nil
                                                  Gather#672 ends at 6.196842ms
                                              Gather#690 step 5/6 (+361ns): 182ns self time
                                              Gather#690 step 6/6 (+543ns): return nil
                                              Gather#690 ends at 6.194995ms
                                          Plan#25 step 5/13 (+0s): scatter:
                                            Task#688: pool=0
                                            Task#688 step 1/2 (+0s): 10.002µs self time
                                            Task#688 step 2/2 (+10.002µs): return nil
                                            Task#688 ends at 10.002µs
                                              Combine#688: index=0 flush=Gather#688
                                              Combine#688 step 1/2 (+0s): 974ns self time
                                              Combine#688 step 2/2 (+974ns): return nil
                                              Combine#688 ends at 10.976µs
                                                Gather#688: index=1
                                                Gather#688 step 1/4 (+0s): 292ns self time
                                                Gather#688 step 2/4 (+292ns): scatter:
                                                  Task#687: pool=1
                                                  Task#687 step 1/2 (+0s): 10.064µs self time
                                                  Task#687 step 2/2 (+10.064µs): return nil
                                                  Task#687 ends at 0s
                                                    Gather#687: index=1
                                                    Gather#687 step 1/4 (+0s): 377.473µs self time
                                                    Gather#687 step 2/4 (+377.473µs): scatter:
                                                      Task#662: pool=1
                                                      Task#662 step 1/2 (+0s): 9.999µs self time
                                                      Task#662 step 2/2 (+9.999µs): return nil
                                                      Task#662 ends at 0s
                                                        Combine#662: index=0 flush=<nil>
                                                        Combine#662 step 1/2 (+0s): 1.006µs self time
                                                        Combine#662 step 2/2 (+1.006µs): return nil
                                                        Combine#662 ends at 0s
                                                    Gather#687 step 3/4 (+377.473µs): 622.527µs self time
                                                    Gather#687 step 4/4 (+1ms): return nil
                                                    Gather#687 ends at 0s
                                                Gather#688 step 3/4 (+292ns): 658ns self time
                                                Gather#688 step 4/4 (+950ns): return nil
                                                Gather#688 ends at 0s
                                          Plan#25 step 6/13 (+0s): scatter:
                                            Task#676: pool=1
                                            Task#676 step 1/2 (+0s): 10.006µs self time
                                            Task#676 step 2/2 (+10.006µs): return nil
                                            Task#676 ends at 10.006µs
                                              Gather#676: index=0
                                              Gather#676 step 1/2 (+0s): 818ns self time
                                              Gather#676 step 2/2 (+818ns): return nil
                                              Gather#676 ends at 10.824µs
                                          Plan#25 step 7/13 (+0s): scatter:
                                            Task#689: pool=1
                                            Task#689 step 1/2 (+0s): 10.1µs self time
                                            Task#689 step 2/2 (+10.1µs): return nil
                                            Task#689 ends at 10.1µs
                                              Gather#689: index=0
                                              Gather#689 step 1/6 (+0s): 321ns self time
                                              Gather#689 step 2/6 (+321ns): scatter:
                                                Task#683: pool=1
                                                Task#683 step 1/2 (+0s): 9.999µs self time
                                                Task#683 step 2/2 (+9.999µs): return nil
                                                Task#683 ends at 20.42µs
                                                  Gather#683: index=0
                                                  Gather#683 step 1/4 (+0s): 438ns self time
                                                  Gather#683 step 2/4 (+438ns): scatter:
                                                    Task#666: pool=1
                                                    Task#666 step 1/2 (+0s): 9.964µs self time
                                                    Task#666 step 2/2 (+9.964µs): return nil
                                                    Task#666 ends at 30.822µs
                                                      Gather#666: index=1
                                                      Gather#666 step 1/2 (+0s): 1.088µs self time
                                                      Gather#666 step 2/2 (+1.088µs): return nil
                                                      Gather#666 ends at 31.91µs
                                                  Gather#683 step 3/4 (+438ns): 562ns self time
                                                  Gather#683 step 4/4 (+1µs): return nil
                                                  Gather#683 ends at 21.42µs
                                              Gather#689 step 3/6 (+321ns): 259ns self time
                                              Gather#689 step 4/6 (+580ns): scatter:
                                                Task#686: pool=1
                                                Task#686 step 1/2 (+0s): 9.656µs self time
                                                Task#686 step 2/2 (+9.656µs): return nil
                                                Task#686 ends at 20.336µs
                                                  Gather#686: index=1
                                                  Gather#686 step 1/4 (+0s): 274.158µs self time
                                                  Gather#686 step 2/4 (+274.158µs): scatter:
                                                    Task#682: pool=1
                                                    Task#682 step 1/2 (+0s): 11.278µs self time
                                                    Task#682 step 2/2 (+11.278µs): return nil
                                                    Task#682 ends at 305.772µs
                                                      Combine#682: index=0 flush=<nil>
                                                      Combine#682 step 1/10 (+0s): 733ns self time
                                                      Combine#682 step 2/10 (+733ns): scatter:
                                                        Task#671: pool=0
                                                        Task#671 step 1/2 (+0s): 9.956µs self time
                                                        Task#671 step 2/2 (+9.956µs): return nil
                                                        Task#671 ends at 316.461µs
                                                          Combine#671: index=0 flush=<nil>
                                                          Combine#671 step 1/2 (+0s): 1µs self time
                                                          Combine#671 step 2/2 (+1µs): return nil
                                                          Combine#671 ends at 317.461µs
                                                      Combine#682 step 3/10 (+733ns): 4ns self time
                                                      Combine#682 step 4/10 (+737ns): scatter:
                                                        Task#663: pool=0
                                                        Task#663 step 1/2 (+0s): 9.999µs self time
                                                        Task#663 step 2/2 (+9.999µs): return error
                                                        Task#663 ends at 316.508µs
                                                          Gather#663: index=0
                                                          Gather#663 step 1/2 (+0s): 5.128µs self time
                                                          Gather#663 step 2/2 (+5.128µs): return nil
                                                          Gather#663 ends at 321.636µs
                                                      Combine#682 step 5/10 (+737ns): 21ns self time
                                                      Combine#682 step 6/10 (+758ns): scatter:
                                                        Task#673: pool=1
                                                        Task#673 step 1/2 (+0s): 9.104231ms self time
                                                        Task#673 step 2/2 (+9.104231ms): return nil
                                                        Task#673 ends at 9.410761ms
                                                          Gather#673: index=0
                                                          Gather#673 step 1/2 (+0s): 4.109µs self time
                                                          Gather#673 step 2/2 (+4.109µs): return nil
                                                          Gather#673 ends at 9.41487ms
                                                      Combine#682 step 7/10 (+758ns): 0s self time
                                                      Combine#682 step 8/10 (+758ns): scatter:
                                                        Task#675: pool=0
                                                        Task#675 step 1/2 (+0s): 9.999µs self time
                                                        Task#675 step 2/2 (+9.999µs): return nil
                                                        Task#675 ends at 316.529µs
                                                          Combine#675: index=0 flush=Gather#675
                                                          Combine#675 step 1/2 (+0s): 1.161µs self time
                                                          Combine#675 step 2/2 (+1.161µs): return nil
                                                          Combine#675 ends at 317.69µs
                                                            Gather#675: index=0
                                                            Gather#675 step 1/2 (+0s): 999ns self time
                                                            Gather#675 step 2/2 (+999ns): return nil
                                                            Gather#675 ends at 0s
                                                      Combine#682 step 9/10 (+758ns): 449ns self time
                                                      Combine#682 step 10/10 (+1.207µs): return nil
                                                      Combine#682 ends at 306.979µs
                                                  Gather#686 step 3/4 (+274.158µs): 274.161µs self time
                                                  Gather#686 step 4/4 (+548.319µs): return nil
                                                  Gather#686 ends at 568.655µs
                                              Gather#689 step 5/6 (+580ns): 403ns self time
                                              Gather#689 step 6/6 (+983ns): return nil
                                              Gather#689 ends at 11.083µs
                                          Plan#25 step 8/13 (+0s): scatter:
                                            Task#664: pool=0
                                            Task#664 step 1/2 (+0s): 9.999µs self time
                                            Task#664 step 2/2 (+9.999µs): return nil
                                            Task#664 ends at 9.999µs
                                              Gather#664: index=1
                                              Gather#664 step 1/2 (+0s): 1.017µs self time
                                              Gather#664 step 2/2 (+1.017µs): return nil
                                              Gather#664 ends at 11.016µs
                                          Plan#25 step 9/13 (+0s): scatter:
                                            Task#674: pool=1
                                            Task#674 step 1/2 (+0s): 9.969µs self time
                                            Task#674 step 2/2 (+9.969µs): return nil
                                            Task#674 ends at 9.969µs
                                              Combine#674: index=0 flush=<nil>
                                              Combine#674 step 1/2 (+0s): 922ns self time
                                              Combine#674 step 2/2 (+922ns): return nil
                                              Combine#674 ends at 10.891µs
                                          Plan#25 step 10/13 (+0s): scatter:
                                            Task#677: pool=0
                                            Task#677 step 1/2 (+0s): 10µs self time
                                            Task#677 step 2/2 (+10µs): return nil
                                            Task#677 ends at 10µs
                                              Gather#677: index=0
                                              Gather#677 step 1/2 (+0s): 1.17µs self time
                                              Gather#677 step 2/2 (+1.17µs): return nil
                                              Gather#677 ends at 11.17µs
                                          Plan#25 step 11/13 (+0s): scatter:
                                            Task#693: pool=0
                                            Task#693 step 1/2 (+0s): 9.957µs self time
                                            Task#693 step 2/2 (+9.957µs): return nil
                                            Task#693 ends at 9.957µs
                                              Gather#693: index=1
                                              Gather#693 step 1/4 (+0s): 501ns self time
                                              Gather#693 step 2/4 (+501ns): scatter:
                                                Task#684: pool=1
                                                Task#684 step 1/2 (+0s): 10.998µs self time
                                                Task#684 step 2/2 (+10.998µs): return nil
                                                Task#684 ends at 21.456µs
                                                  Gather#684: index=1
                                                  Gather#684 step 1/4 (+0s): 515ns self time
                                                  Gather#684 step 2/4 (+515ns): scatter:
                                                    Task#669: pool=1
                                                    Task#669 step 1/2 (+0s): 9.999µs self time
                                                    Task#669 step 2/2 (+9.999µs): return nil
                                                    Task#669 ends at 31.97µs
                                                      Gather#669: index=0
                                                      Gather#669 step 1/2 (+0s): 993ns self time
                                                      Gather#669 step 2/2 (+993ns): return nil
                                                      Gather#669 ends at 32.963µs
                                                  Gather#684 step 3/4 (+515ns): 517ns self time
                                                  Gather#684 step 4/4 (+1.032µs): return nil
                                                  Gather#684 ends at 22.488µs
                                              Gather#693 step 3/4 (+501ns): 498ns self time
                                              Gather#693 step 4/4 (+999ns): return nil
                                              Gather#693 ends at 10.956µs
                                          Plan#25 step 12/13 (+0s): scatter:
                                            Task#691: pool=0
                                            Task#691 step 1/2 (+0s): 10.005µs self time
                                            Task#691 step 2/2 (+10.005µs): return nil
                                            Task#691 ends at 10.005µs
                                              Combine#691: index=0 flush=<nil>
                                              Combine#691 step 1/4 (+0s): 720ns self time
                                              Combine#691 step 2/4 (+720ns): scatter:
                                                Task#685: pool=0
                                                Task#685 step 1/2 (+0s): 9.998µs self time
                                                Task#685 step 2/2 (+9.998µs): return nil
                                                Task#685 ends at 20.723µs
                                                  Combine#685: index=0 flush=<nil>
                                                  Combine#685 step 1/10 (+0s): 186ns self time
                                                  Combine#685 step 2/10 (+186ns): scatter:
                                                    Task#667: pool=0
                                                    Task#667 step 1/2 (+0s): 947ns self time
                                                    Task#667 step 2/2 (+947ns): return nil
                                                    Task#667 ends at 21.856µs
                                                      Gather#667: index=0
                                                      Gather#667 step 1/2 (+0s): 65.76µs self time
                                                      Gather#667 step 2/2 (+65.76µs): return nil
                                                      Gather#667 ends at 87.616µs
                                                  Combine#685 step 3/10 (+186ns): 162ns self time
                                                  Combine#685 step 4/10 (+348ns): scatter:
                                                    Task#681: pool=1
                                                    Task#681 step 1/2 (+0s): 8.672µs self time
                                                    Task#681 step 2/2 (+8.672µs): return nil
                                                    Task#681 ends at 29.743µs
                                                      Gather#681: index=1
                                                      Gather#681 step 1/2 (+0s): 326.651µs self time
                                                      Gather#681 step 2/2 (+326.651µs): return nil
                                                      Gather#681 ends at 356.394µs
                                                  Combine#685 step 5/10 (+348ns): 221ns self time
                                                  Combine#685 step 6/10 (+569ns): scatter:
                                                    Task#680: pool=0
                                                    Task#680 step 1/2 (+0s): 9.996µs self time
                                                    Task#680 step 2/2 (+9.996µs): return nil
                                                    Task#680 ends at 31.288µs
                                                      Combine#680: index=0 flush=<nil>
                                                      Combine#680 step 1/2 (+0s): 990ns self time
                                                      Combine#680 step 2/2 (+990ns): return nil
                                                      Combine#680 ends at 32.278µs
                                                  Combine#685 step 7/10 (+569ns): 268ns self time
                                                  Combine#685 step 8/10 (+837ns): scatter:
                                                    Task#670: pool=0
                                                    Task#670 step 1/2 (+0s): 9.998µs self time
                                                    Task#670 step 2/2 (+9.998µs): return nil
                                                    Task#670 ends at 31.558µs
                                                      Combine#670: index=0 flush=<nil>
                                                      Combine#670 step 1/2 (+0s): 999ns self time
                                                      Combine#670 step 2/2 (+999ns): return nil
                                                      Combine#670 ends at 32.557µs
                                                  Combine#685 step 9/10 (+837ns): 173ns self time
                                                  Combine#685 step 10/10 (+1.01µs): return nil
                                                  Combine#685 ends at 21.733µs
                                              Combine#691 step 3/4 (+720ns): 87ns self time
                                              Combine#691 step 4/4 (+807ns): return nil
                                              Combine#691 ends at 10.812µs
                                          Plan#25 step 13/13 (+0s): ends at 9.41487ms
                                        Gather#661 step 7/16 (+9.415102ms): 709ns self time
                                        Gather#661 step 8/16 (+9.415811ms): scatter:
                                          Task#619: pool=2
                                          Task#619 step 1/2 (+0s): 511.269µs self time
                                          Task#619 step 2/2 (+511.269µs): return nil
                                          Task#619 ends at 9.984075ms
                                            Gather#619: index=3
                                            Gather#619 step 1/2 (+0s): 982ns self time
                                            Gather#619 step 2/2 (+982ns): return nil
                                            Gather#619 ends at 9.985057ms
                                        Gather#661 step 9/16 (+9.415811ms): 56ns self time
                                        Gather#661 step 10/16 (+9.415867ms): scatter:
                                          Task#660: pool=0
                                          Task#660 step 1/2 (+0s): 9.356µs self time
                                          Task#660 step 2/2 (+9.356µs): return nil
                                          Task#660 ends at 9.482218ms
                                            Gather#660: index=2
                                            Gather#660 step 1/10 (+0s): 146ns self time
                                            Gather#660 step 2/10 (+146ns): scatter:
                                              Task#617: pool=1
                                              Task#617 step 1/2 (+0s): 377ns self time
                                              Task#617 step 2/2 (+377ns): return nil
                                              Task#617 ends at 9.482741ms
                                                Gather#617: index=7
                                                Gather#617 step 1/2 (+0s): 1.108µs self time
                                                Gather#617 step 2/2 (+1.108µs): return nil
                                                Gather#617 ends at 9.483849ms
                                            Gather#660 step 3/10 (+146ns): 193ns self time
                                            Gather#660 step 4/10 (+339ns): scatter:
                                              Task#657: pool=2
                                              Task#657 step 1/2 (+0s): 10.062µs self time
                                              Task#657 step 2/2 (+10.062µs): return nil
                                              Task#657 ends at 9.492619ms
                                                Gather#657: index=2
                                                Gather#657 step 1/2 (+0s): 994ns self time
                                                Gather#657 step 2/2 (+994ns): return nil
                                                Gather#657 ends at 9.493613ms
                                            Gather#660 step 5/10 (+339ns): 136ns self time
                                            Gather#660 step 6/10 (+475ns): scatter:
                                              Task#620: pool=1
                                              Task#620 step 1/4 (+0s): 1.353µs self time
                                              Task#620 step 2/4 (+1.353µs): subjob:
                                                Plan#24: pathCount=22 taskCount=33 maxPathDuration=9.876581ms minGatherCount=22 maxGatherCount=33
                                                   TaskPools[0]: TaskPool#71: limit=7
                                                   CombinerPools[0]: CombinerPool#125: limit=1
                                                   Combiners[0]: pool=0
                                                   Combiners[1]: pool=0
                                                   Combiners[2]: pool=0
                                                   Combiners[3]: pool=0
                                                   Combiners[4]: pool=0
                                                   Combiners[5]: pool=0
                                                   Combiners[6]: pool=0
                                                   Combiners[7]: pool=0
                                                   Combiners[8]: pool=0
                                                   Combiners[9]: pool=0
                                                   Combiners[10]: pool=0
                                                   Combiners[11]: pool=0
                                                   Combiners[12]: pool=0
                                                   Combiners[13]: pool=0
                                                   Combiners[14]: pool=0
                                                Plan#24 step 1/4 (+0s): scatter:
                                                  Task#633: pool=0
                                                  Task#633 step 1/2 (+0s): 9.595µs self time
                                                  Task#633 step 2/2 (+9.595µs): return nil
                                                  Task#633 ends at 9.595µs
                                                    Combine#633: index=12 flush=<nil>
                                                    Combine#633 step 1/2 (+0s): 1.066µs self time
                                                    Combine#633 step 2/2 (+1.066µs): return nil
                                                    Combine#633 ends at 10.661µs
                                                Plan#24 step 2/4 (+0s): scatter:
                                                  Task#623: pool=0
                                                  Task#623 step 1/2 (+0s): 10.004µs self time
                                                  Task#623 step 2/2 (+10.004µs): return nil
                                                  Task#623 ends at 10.004µs
                                                    Gather#623: index=0
                                                    Gather#623 step 1/2 (+0s): 1.063µs self time
                                                    Gather#623 step 2/2 (+1.063µs): return nil
                                                    Gather#623 ends at 11.067µs
                                                Plan#24 step 3/4 (+0s): scatter:
                                                  Task#653: pool=0
                                                  Task#653 step 1/2 (+0s): 10.027µs self time
                                                  Task#653 step 2/2 (+10.027µs): return nil
                                                  Task#653 ends at 10.027µs
                                                    Combine#653: index=13 flush=<nil>
                                                    Combine#653 step 1/22 (+0s): 50ns self time
                                                    Combine#653 step 2/22 (+50ns): scatter:
                                                      Task#649: pool=0
                                                      Task#649 step 1/2 (+0s): 9.993µs self time
                                                      Task#649 step 2/2 (+9.993µs): return nil
                                                      Task#649 ends at 20.07µs
                                                        Gather#649: index=0
                                                        Gather#649 step 1/4 (+0s): 1.045µs self time
                                                        Gather#649 step 2/4 (+1.045µs): scatter:
                                                          Task#622: pool=0
                                                          Task#622 step 1/2 (+0s): 108.52µs self time
                                                          Task#622 step 2/2 (+108.52µs): return nil
                                                          Task#622 ends at 129.635µs
                                                            Gather#622: index=0
                                                            Gather#622 step 1/2 (+0s): 957ns self time
                                                            Gather#622 step 2/2 (+957ns): return nil
                                                            Gather#622 ends at 130.592µs
                                                        Gather#649 step 3/4 (+1.045µs): 1.048µs self time
                                                        Gather#649 step 4/4 (+2.093µs): return nil
                                                        Gather#649 ends at 22.163µs
                                                    Combine#653 step 3/22 (+50ns): 13ns self time
                                                    Combine#653 step 4/22 (+63ns): scatter:
                                                      Task#650: pool=0
                                                      Task#650 step 1/2 (+0s): 9.768µs self time
                                                      Task#650 step 2/2 (+9.768µs): return nil
                                                      Task#650 ends at 19.858µs
                                                        Gather#650: index=0
                                                        Gather#650 step 1/4 (+0s): 2ns self time
                                                        Gather#650 step 2/4 (+2ns): scatter:
                                                          Task#648: pool=0
                                                          Task#648 step 1/2 (+0s): 9.997µs self time
                                                          Task#648 step 2/2 (+9.997µs): return nil
                                                          Task#648 ends at 29.857µs
                                                            Combine#648: index=12 flush=<nil>
                                                            Combine#648 step 1/22 (+0s): 82ns self time
                                                            Combine#648 step 2/22 (+82ns): scatter:
                                                              Task#647: pool=0
                                                              Task#647 step 1/2 (+0s): 9.986µs self time
                                                              Task#647 step 2/2 (+9.986µs): return nil
                                                              Task#647 ends at 39.925µs
                                                                Combine#647: index=4 flush=<nil>
                                                                Combine#647 step 1/4 (+0s): 641ns self time
                                                                Combine#647 step 2/4 (+641ns): scatter:
                                                                  Task#642: pool=0
                                                                  Task#642 step 1/2 (+0s): 9.998µs self time
                                                                  Task#642 step 2/2 (+9.998µs): return nil
                                                                  Task#642 ends at 50.564µs
                                                                    Gather#642: index=0
                                                                    Gather#642 step 1/2 (+0s): 0s self time
                                                                    Gather#642 step 2/2 (+0s): return nil
                                                                    Gather#642 ends at 50.564µs
                                                                Combine#647 step 3/4 (+641ns): 358ns self time
                                                                Combine#647 step 4/4 (+999ns): return nil
                                                                Combine#647 ends at 40.924µs
                                                            Combine#648 step 3/22 (+82ns): 85ns self time
                                                            Combine#648 step 4/22 (+167ns): scatter:
                                                              Task#645: pool=0
                                                              Task#645 step 1/2 (+0s): 38.702µs self time
                                                              Task#645 step 2/2 (+38.702µs): return error
                                                              Task#645 ends at 68.726µs
                                                                Gather#645: index=0
                                                                Gather#645 step 1/4 (+0s): 475ns self time
                                                                Gather#645 step 2/4 (+475ns): scatter:
                                                                  Task#629: pool=0
                                                                  Task#629 step 1/2 (+0s): 10.001µs self time
                                                                  Task#629 step 2/2 (+10.001µs): return nil
                                                                  Task#629 ends at 79.202µs
                                                                    Combine#629: index=1 flush=<nil>
                                                                    Combine#629 step 1/2 (+0s): 17.47µs self time
                                                                    Combine#629 step 2/2 (+17.47µs): return nil
                                                                    Combine#629 ends at 96.672µs
                                                                Gather#645 step 3/4 (+475ns): 260ns self time
                                                                Gather#645 step 4/4 (+735ns): return nil
                                                                Gather#645 ends at 69.461µs
                                                            Combine#648 step 5/22 (+167ns): 89ns self time
                                                            Combine#648 step 6/22 (+256ns): scatter:
                                                              Task#643: pool=0
                                                              Task#643 step 1/2 (+0s): 9.998µs self time
                                                              Task#643 step 2/2 (+9.998µs): return nil
                                                              Task#643 ends at 40.111µs
                                                                Gather#643: index=0
                                                                Gather#643 step 1/4 (+0s): 508ns self time
                                                                Gather#643 step 2/4 (+508ns): scatter:
                                                                  Task#628: pool=0
                                                                  Task#628 step 1/2 (+0s): 29.868µs self time
                                                                  Task#628 step 2/2 (+29.868µs): return nil
                                                                  Task#628 ends at 70.487µs
                                                                    Gather#628: index=0
                                                                    Gather#628 step 1/2 (+0s): 1.003µs self time
                                                                    Gather#628 step 2/2 (+1.003µs): return nil
                                                                    Gather#628 ends at 71.49µs
                                                                Gather#643 step 3/4 (+508ns): 510ns self time
                                                                Gather#643 step 4/4 (+1.018µs): return nil
                                                                Gather#643 ends at 41.129µs
                                                            Combine#648 step 7/22 (+256ns): 92ns self time
                                                            Combine#648 step 8/22 (+348ns): scatter:
                                                              Task#624: pool=0
                                                              Task#624 step 1/2 (+0s): 10.03µs self time
                                                              Task#624 step 2/2 (+10.03µs): return error
                                                              Task#624 ends at 40.235µs
                                                                Combine#624: index=1 flush=<nil>
                                                                Combine#624 step 1/2 (+0s): 1.001µs self time
                                                                Combine#624 step 2/2 (+1.001µs): return nil
                                                                Combine#624 ends at 41.236µs
                                                            Combine#648 step 9/22 (+348ns): 90ns self time
                                                            Combine#648 step 10/22 (+438ns): scatter:
                                                              Task#632: pool=0
                                                              Task#632 step 1/2 (+0s): 10.97µs self time
                                                              Task#632 step 2/2 (+10.97µs): return nil
                                                              Task#632 ends at 41.265µs
                                                                Gather#632: index=0
                                                                Gather#632 step 1/2 (+0s): 1.003µs self time
                                                                Gather#632 step 2/2 (+1.003µs): return nil
                                                                Gather#632 ends at 42.268µs
                                                            Combine#648 step 11/22 (+438ns): 89ns self time
                                                            Combine#648 step 12/22 (+527ns): scatter:
                                                              Task#646: pool=0
                                                              Task#646 step 1/2 (+0s): 9.989µs self time
                                                              Task#646 step 2/2 (+9.989µs): return nil
                                                              Task#646 ends at 40.373µs
                                                                Gather#646: index=0
                                                                Gather#646 step 1/4 (+0s): 499ns self time
                                                                Gather#646 step 2/4 (+499ns): scatter:
                                                                  Task#641: pool=0
                                                                  Task#641 step 1/2 (+0s): 9.996µs self time
                                                                  Task#641 step 2/2 (+9.996µs): return nil
                                                                  Task#641 ends at 50.868µs
                                                                    Gather#641: index=0
                                                                    Gather#641 step 1/2 (+0s): 743ns self time
                                                                    Gather#641 step 2/2 (+743ns): return nil
                                                                    Gather#641 ends at 51.611µs
                                                                Gather#646 step 3/4 (+499ns): 500ns self time
                                                                Gather#646 step 4/4 (+999ns): return nil
                                                                Gather#646 ends at 41.372µs
                                                            Combine#648 step 13/22 (+527ns): 93ns self time
                                                            Combine#648 step 14/22 (+620ns): scatter:
                                                              Task#644: pool=0
                                                              Task#644 step 1/2 (+0s): 34.51µs self time
                                                              Task#644 step 2/2 (+34.51µs): return nil
                                                              Task#644 ends at 64.987µs
                                                                Gather#644: index=0
                                                                Gather#644 step 1/6 (+0s): 0s self time
                                                                Gather#644 step 2/6 (+0s): scatter:
                                                                  Task#637: pool=0
                                                                  Task#637 step 1/2 (+0s): 8.437339ms self time
                                                                  Task#637 step 2/2 (+8.437339ms): return nil
                                                                  Task#637 ends at 8.502326ms
                                                                    Gather#637: index=0
                                                                    Gather#637 step 1/2 (+0s): 120.474µs self time
                                                                    Gather#637 step 2/2 (+120.474µs): return nil
                                                                    Gather#637 ends at 8.6228ms
                                                                Gather#644 step 3/6 (+0s): 530ns self time
                                                                Gather#644 step 4/6 (+530ns): scatter:
                                                                  Task#627: pool=0
                                                                  Task#627 step 1/2 (+0s): 8.811064ms self time
                                                                  Task#627 step 2/2 (+8.811064ms): return nil
                                                                  Task#627 ends at 8.876581ms
                                                                    Combine#627: index=9 flush=<nil>
                                                                    Combine#627 step 1/2 (+0s): 1ms self time
                                                                    Combine#627 step 2/2 (+1ms): return nil
                                                                    Combine#627 ends at 9.876581ms
                                                                Gather#644 step 5/6 (+530ns): 521ns self time
                                                                Gather#644 step 6/6 (+1.051µs): return nil
                                                                Gather#644 ends at 66.038µs
                                                            Combine#648 step 15/22 (+620ns): 124ns self time
                                                            Combine#648 step 16/22 (+744ns): scatter:
                                                              Task#630: pool=0
                                                              Task#630 step 1/2 (+0s): 10.073µs self time
                                                              Task#630 step 2/2 (+10.073µs): return nil
                                                              Task#630 ends at 40.674µs
                                                                Gather#630: index=0
                                                                Gather#630 step 1/2 (+0s): 1.002µs self time
                                                                Gather#630 step 2/2 (+1.002µs): return nil
                                                                Gather#630 ends at 41.676µs
                                                            Combine#648 step 17/22 (+744ns): 183ns self time
                                                            Combine#648 step 18/22 (+927ns): scatter:
                                                              Task#634: pool=0
                                                              Task#634 step 1/2 (+0s): 10.081µs self time
                                                              Task#634 step 2/2 (+10.081µs): return nil
                                                              Task#634 ends at 40.865µs
                                                                Combine#634: index=2 flush=Gather#634
                                                                Combine#634 step 1/2 (+0s): 996ns self time
                                                                Combine#634 step 2/2 (+996ns): return nil
                                                                Combine#634 ends at 41.861µs
                                                                  Gather#634: index=0
                                                                  Gather#634 step 1/2 (+0s): 321.554µs self time
                                                                  Gather#634 step 2/2 (+321.554µs): return nil
                                                                  Gather#634 ends at 0s
                                                            Combine#648 step 19/22 (+927ns): 31ns self time
                                                            Combine#648 step 20/22 (+958ns): scatter:
                                                              Task#626: pool=0
                                                              Task#626 step 1/2 (+0s): 10.001µs self time
                                                              Task#626 step 2/2 (+10.001µs): return error
                                                              Task#626 ends at 40.816µs
                                                                Gather#626: index=0
                                                                Gather#626 step 1/2 (+0s): 0s self time
                                                                Gather#626 step 2/2 (+0s): return nil
                                                                Gather#626 ends at 40.816µs
                                                            Combine#648 step 21/22 (+958ns): 25ns self time
                                                            Combine#648 step 22/22 (+983ns): return nil
                                                            Combine#648 ends at 30.84µs
                                                        Gather#650 step 3/4 (+2ns): 3ns self time
                                                        Gather#650 step 4/4 (+5ns): return nil
                                                        Gather#650 ends at 19.863µs
                                                    Combine#653 step 5/22 (+63ns): 35ns self time
                                                    Combine#653 step 6/22 (+98ns): scatter:
                                                      Task#652: pool=0
                                                      Task#652 step 1/2 (+0s): 10.492µs self time
                                                      Task#652 step 2/2 (+10.492µs): return nil
                                                      Task#652 ends at 20.617µs
                                                        Gather#652: index=0
                                                        Gather#652 step 1/4 (+0s): 497ns self time
                                                        Gather#652 step 2/4 (+497ns): scatter:
                                                          Task#621: pool=0
                                                          Task#621 step 1/2 (+0s): 230.72µs self time
                                                          Task#621 step 2/2 (+230.72µs): return error
                                                          Task#621 ends at 251.834µs
                                                            Gather#621: index=0
                                                            Gather#621 step 1/2 (+0s): 1.022µs self time
                                                            Gather#621 step 2/2 (+1.022µs): return nil
                                                            Gather#621 ends at 252.856µs
                                                        Gather#652 step 3/4 (+497ns): 501ns self time
                                                        Gather#652 step 4/4 (+998ns): return nil
                                                        Gather#652 ends at 21.615µs
                                                    Combine#653 step 7/22 (+98ns): 84ns self time
                                                    Combine#653 step 8/22 (+182ns): scatter:
                                                      Task#651: pool=0
                                                      Task#651 step 1/2 (+0s): 9.997µs self time
                                                      Task#651 step 2/2 (+9.997µs): return nil
                                                      Task#651 ends at 20.206µs
                                                        Combine#651: index=12 flush=<nil>
                                                        Combine#651 step 1/4 (+0s): 495ns self time
                                                        Combine#651 step 2/4 (+495ns): scatter:
                                                          Task#638: pool=0
                                                          Task#638 step 1/2 (+0s): 684.175µs self time
                                                          Task#638 step 2/2 (+684.175µs): return nil
                                                          Task#638 ends at 704.876µs
                                                            Combine#638: index=2 flush=<nil>
                                                            Combine#638 step 1/2 (+0s): 936ns self time
                                                            Combine#638 step 2/2 (+936ns): return nil
                                                            Combine#638 ends at 705.812µs
                                                        Combine#651 step 3/4 (+495ns): 503ns self time
                                                        Combine#651 step 4/4 (+998ns): return nil
                                                        Combine#651 ends at 21.204µs
                                                    Combine#653 step 9/22 (+182ns): 97ns self time
                                                    Combine#653 step 10/22 (+279ns): scatter:
                                                      Task#635: pool=0
                                                      Task#635 step 1/2 (+0s): 10.001µs self time
                                                      Task#635 step 2/2 (+10.001µs): return nil
                                                      Task#635 ends at 20.307µs
                                                        Gather#635: index=0
                                                        Gather#635 step 1/2 (+0s): 989ns self time
                                                        Gather#635 step 2/2 (+989ns): return nil
                                                        Gather#635 ends at 21.296µs
                                                    Combine#653 step 11/22 (+279ns): 63ns self time
                                                    Combine#653 step 12/22 (+342ns): scatter:
                                                      Task#636: pool=0
                                                      Task#636 step 1/2 (+0s): 9.999µs self time
                                                      Task#636 step 2/2 (+9.999µs): return nil
                                                      Task#636 ends at 20.368µs
                                                        Gather#636: index=0
                                                        Gather#636 step 1/2 (+0s): 850ns self time
                                                        Gather#636 step 2/2 (+850ns): return nil
                                                        Gather#636 ends at 21.218µs
                                                    Combine#653 step 13/22 (+342ns): 130ns self time
                                                    Combine#653 step 14/22 (+472ns): scatter:
                                                      Task#640: pool=0
                                                      Task#640 step 1/2 (+0s): 9.942µs self time
                                                      Task#640 step 2/2 (+9.942µs): return nil
                                                      Task#640 ends at 20.441µs
                                                        Combine#640: index=2 flush=<nil>
                                                        Combine#640 step 1/2 (+0s): 998ns self time
                                                        Combine#640 step 2/2 (+998ns): return nil
                                                        Combine#640 ends at 21.439µs
                                                    Combine#653 step 15/22 (+472ns): 104ns self time
                                                    Combine#653 step 16/22 (+576ns): scatter:
                                                      Task#631: pool=0
                                                      Task#631 step 1/2 (+0s): 9.948µs self time
                                                      Task#631 step 2/2 (+9.948µs): return nil
                                                      Task#631 ends at 20.551µs
                                                        Gather#631: index=0
                                                        Gather#631 step 1/2 (+0s): 997ns self time
                                                        Gather#631 step 2/2 (+997ns): return nil
                                                        Gather#631 ends at 21.548µs
                                                    Combine#653 step 17/22 (+576ns): 141ns self time
                                                    Combine#653 step 18/22 (+717ns): scatter:
                                                      Task#639: pool=0
                                                      Task#639 step 1/2 (+0s): 9.97µs self time
                                                      Task#639 step 2/2 (+9.97µs): return nil
                                                      Task#639 ends at 20.714µs
                                                        Combine#639: index=2 flush=<nil>
                                                        Combine#639 step 1/2 (+0s): 1.053µs self time
                                                        Combine#639 step 2/2 (+1.053µs): return nil
                                                        Combine#639 ends at 21.767µs
                                                    Combine#653 step 19/22 (+717ns): 142ns self time
                                                    Combine#653 step 20/22 (+859ns): scatter:
                                                      Task#625: pool=0
                                                      Task#625 step 1/2 (+0s): 10.438µs self time
                                                      Task#625 step 2/2 (+10.438µs): return nil
                                                      Task#625 ends at 21.324µs
                                                        Gather#625: index=0
                                                        Gather#625 step 1/2 (+0s): 1.004µs self time
                                                        Gather#625 step 2/2 (+1.004µs): return nil
                                                        Gather#625 ends at 22.328µs
                                                    Combine#653 step 21/22 (+859ns): 142ns self time
                                                    Combine#653 step 22/22 (+1.001µs): return nil
                                                    Combine#653 ends at 11.028µs
                                                Plan#24 step 4/4 (+0s): ends at 9.876581ms
                                              Task#620 step 3/4 (+9.877934ms): 1.154µs self time
                                              Task#620 step 4/4 (+9.879088ms): return nil
                                              Task#620 ends at 19.361781ms
                                                Gather#620: index=7
                                                Gather#620 step 1/2 (+0s): 998ns self time
                                                Gather#620 step 2/2 (+998ns): return nil
                                                Gather#620 ends at 19.362779ms
                                            Gather#660 step 7/10 (+475ns): 114ns self time
                                            Gather#660 step 8/10 (+589ns): scatter:
                                              Task#616: pool=0
                                              Task#616 step 1/2 (+0s): 9.999µs self time
                                              Task#616 step 2/2 (+9.999µs): return nil
                                              Task#616 ends at 9.492806ms
                                                Combine#616: index=5 flush=<nil>
                                                Combine#616 step 1/2 (+0s): 852ns self time
                                                Combine#616 step 2/2 (+852ns): return nil
                                                Combine#616 ends at 9.493658ms
                                            Gather#660 step 9/10 (+589ns): 411ns self time
                                            Gather#660 step 10/10 (+1µs): return nil
                                            Gather#660 ends at 9.483218ms
                                        Gather#661 step 11/16 (+9.415867ms): 3ns self time
                                        Gather#661 step 12/16 (+9.41587ms): scatter:
                                          Task#654: pool=2
                                          Task#654 step 1/2 (+0s): 6.809µs self time
                                          Task#654 step 2/2 (+6.809µs): return nil
                                          Task#654 ends at 9.479674ms
                                            Gather#654: index=6
                                            Gather#654 step 1/2 (+0s): 1µs self time
                                            Gather#654 step 2/2 (+1µs): return nil
                                            Gather#654 ends at 9.480674ms
                                        Gather#661 step 13/16 (+9.41587ms): 4ns self time
                                        Gather#661 step 14/16 (+9.415874ms): scatter:
                                          Task#547: pool=0
                                          Task#547 step 1/2 (+0s): 1.071µs self time
                                          Task#547 step 2/2 (+1.071µs): return nil
                                          Task#547 ends at 9.47394ms
                                            Gather#547: index=1
                                            Gather#547 step 1/2 (+0s): 523.863µs self time
                                            Gather#547 step 2/2 (+523.863µs): return nil
                                            Gather#547 ends at 9.997803ms
                                        Gather#661 step 15/16 (+9.415874ms): 30ns self time
                                        Gather#661 step 16/16 (+9.415904ms): return nil
                                        Gather#661 ends at 9.472899ms
                                    Gather#694 step 3/4 (+26.591µs): 27.894µs self time
                                    Gather#694 step 4/4 (+54.485µs): return nil
                                    Gather#694 ends at 74.892µs
                                Gather#696 step 3/4 (+436ns): 451ns self time
                                Gather#696 step 4/4 (+887ns): return nil
                                Gather#696 ends at 10.888µs
                            Plan#21 step 4/6 (+0s): scatter:
                              Task#618: pool=1
                              Task#618 step 1/2 (+0s): 9.344456ms self time
                              Task#618 step 2/2 (+9.344456ms): return nil
                              Task#618 ends at 9.344456ms
                                Gather#618: index=6
                                Gather#618 step 1/2 (+0s): 1µs self time
                                Gather#618 step 2/2 (+1µs): return nil
                                Gather#618 ends at 9.345456ms
                            Plan#21 step 5/6 (+0s): scatter:
                              Task#655: pool=2
                              Task#655 step 1/2 (+0s): 10.015µs self time
                              Task#655 step 2/2 (+10.015µs): return nil
                              Task#655 ends at 10.015µs
                                Gather#655: index=4
                                Gather#655 step 1/2 (+0s): 1ms self time
                                Gather#655 step 2/2 (+1ms): return nil
                                Gather#655 ends at 1.010015ms
                            Plan#21 step 6/6 (+0s): ends at 20.304723ms
                          Gather#546 step 3/6 (+20.305066ms): 581ns self time
                          Gather#546 step 4/6 (+20.305647ms): scatter:
                            Task#277: pool=0
                            Task#277 step 1/2 (+0s): 9.996µs self time
                            Task#277 step 2/2 (+9.996µs): return nil
                            Task#277 ends at 20.346675ms
                              Combine#277: index=0 flush=<nil>
                              Combine#277 step 1/2 (+0s): 1.002µs self time
                              Combine#277 step 2/2 (+1.002µs): return nil
                              Combine#277 ends at 20.347677ms
                          Gather#546 step 5/6 (+20.305647ms): 77ns self time
                          Gather#546 step 6/6 (+20.305724ms): return nil
                          Gather#546 ends at 20.336756ms
                      Gather#703 step 9/10 (+1.032µs): 9ns self time
                      Gather#703 step 10/10 (+1.041µs): return error
                      Gather#703 ends at 21.044µs
                  Gather#897 step 3/6 (+0s): 1.229µs self time
                  Gather#897 step 4/6 (+1.229µs): scatter:
                    Task#280: pool=0
                    Task#280 step 1/2 (+0s): 9.999µs self time
                    Task#280 step 2/2 (+9.999µs): return error
                    Task#280 ends at 21.23µs
                      Gather#280: index=0
                      Gather#280 step 1/2 (+0s): 2.466µs self time
                      Gather#280 step 2/2 (+2.466µs): return nil
                      Gather#280 ends at 23.696µs
                  Gather#897 step 5/6 (+1.229µs): 1.228µs self time
                  Gather#897 step 6/6 (+2.457µs): return nil
                  Gather#897 ends at 12.459µs
              Plan#12 step 5/10 (+0s): scatter:
                Task#279: pool=0
                Task#279 step 1/2 (+0s): 9.995µs self time
                Task#279 step 2/2 (+9.995µs): return nil
                Task#279 ends at 9.995µs
                  Gather#279: index=2
                  Gather#279 step 1/2 (+0s): 1.033µs self time
                  Gather#279 step 2/2 (+1.033µs): return nil
                  Gather#279 ends at 11.028µs
              Plan#12 step 6/10 (+0s): scatter:
                Task#899: pool=0
                Task#899 step 1/2 (+0s): 5.025437ms self time
                Task#899 step 2/2 (+5.025437ms): return nil
                Task#899 ends at 5.025437ms
                  Gather#899: index=2
                  Gather#899 step 1/4 (+0s): 507ns self time
                  Gather#899 step 2/4 (+507ns): scatter:
                    Task#704: pool=0
                    Task#704 step 1/2 (+0s): 9.993µs self time
                    Task#704 step 2/2 (+9.993µs): return nil
                    Task#704 ends at 5.035937ms
                      Gather#704: index=1
                      Gather#704 step 1/6 (+0s): 276ns self time
                      Gather#704 step 2/6 (+276ns): scatter:
                        Task#278: pool=0
                        Task#278 step 1/2 (+0s): 10.097µs self time
                        Task#278 step 2/2 (+10.097µs): return nil
                        Task#278 ends at 5.04631ms
                          Combine#278: index=0 flush=<nil>
                          Combine#278 step 1/2 (+0s): 999ns self time
                          Combine#278 step 2/2 (+999ns): return nil
                          Combine#278 ends at 5.047309ms
                      Gather#704 step 3/6 (+276ns): 470ns self time
                      Gather#704 step 4/6 (+746ns): subjob:
                        Plan#26: pathCount=21 taskCount=28 maxPathDuration=28.307391ms minGatherCount=19 maxGatherCount=109
                           TaskPools[0]: TaskPool#74: limit=9
                           TaskPools[1]: TaskPool#75: limit=1
                           TaskPools[2]: TaskPool#76: limit=1
                           TaskPools[3]: TaskPool#77: limit=10
                           TaskPools[4]: TaskPool#78: limit=3
                           CombinerPools[0]: CombinerPool#129: limit=2
                           CombinerPools[1]: CombinerPool#130: limit=2
                           CombinerPools[2]: CombinerPool#131: limit=10
                           CombinerPools[3]: CombinerPool#132: limit=7
                           CombinerPools[4]: CombinerPool#133: limit=2
                           Combiners[0]: pool=2
                        Plan#26 step 1/9 (+0s): scatter:
                          Task#894: pool=0
                          Task#894 step 1/2 (+0s): 6.77981ms self time
                          Task#894 step 2/2 (+6.77981ms): return error
                          Task#894 ends at 6.77981ms
                            Combine#894: index=0 flush=<nil>
                            Combine#894 step 1/16 (+0s): 124ns self time
                            Combine#894 step 2/16 (+124ns): scatter:
                              Task#833: pool=4
                              Task#833 step 1/2 (+0s): 12.774µs self time
                              Task#833 step 2/2 (+12.774µs): return nil
                              Task#833 ends at 6.792708ms
                                Gather#833: index=1
                                Gather#833 step 1/2 (+0s): 1µs self time
                                Gather#833 step 2/2 (+1µs): return nil
                                Gather#833 ends at 6.793708ms
                            Combine#894 step 3/16 (+124ns): 56ns self time
                            Combine#894 step 4/16 (+180ns): scatter:
                              Task#706: pool=1
                              Task#706 step 1/2 (+0s): 9.843µs self time
                              Task#706 step 2/2 (+9.843µs): return nil
                              Task#706 ends at 6.789833ms
                                Combine#706: index=0 flush=<nil>
                                Combine#706 step 1/2 (+0s): 997ns self time
                                Combine#706 step 2/2 (+997ns): return nil
                                Combine#706 ends at 6.79083ms
                            Combine#894 step 5/16 (+180ns): 0s self time
                            Combine#894 step 6/16 (+180ns): scatter:
                              Task#713: pool=0
                              Task#713 step 1/2 (+0s): 9.761µs self time
                              Task#713 step 2/2 (+9.761µs): return nil
                              Task#713 ends at 6.789751ms
                                Gather#713: index=3
                                Gather#713 step 1/4 (+0s): 8ns self time
                                Gather#713 step 2/4 (+8ns): subjob:
                                  Plan#27: pathCount=10 taskCount=16 maxPathDuration=869.878µs minGatherCount=11 maxGatherCount=24
                                     TaskPools[0]: TaskPool#79: limit=3
                                     TaskPools[1]: TaskPool#80: limit=2
                                     TaskPools[2]: TaskPool#81: limit=7
                                     TaskPools[3]: TaskPool#82: limit=1
                                     TaskPools[4]: TaskPool#83: limit=8
                                     TaskPools[5]: TaskPool#84: limit=2
                                     TaskPools[6]: TaskPool#85: limit=2
                                     CombinerPools[0]: CombinerPool#134: limit=5
                                     CombinerPools[1]: CombinerPool#135: limit=2
                                     Combiners[0]: pool=1
                                     Combiners[1]: pool=1
                                     Combiners[2]: pool=0
                                     Combiners[3]: pool=1
                                     Combiners[4]: pool=1
                                     Combiners[5]: pool=1
                                  Plan#27 step 1/4 (+0s): scatter:
                                    Task#715: pool=1
                                    Task#715 step 1/2 (+0s): 9.913µs self time
                                    Task#715 step 2/2 (+9.913µs): return nil
                                    Task#715 ends at 9.913µs
                                      Gather#715: index=5
                                      Gather#715 step 1/2 (+0s): 661ns self time
                                      Gather#715 step 2/2 (+661ns): return nil
                                      Gather#715 ends at 10.574µs
                                  Plan#27 step 2/4 (+0s): scatter:
                                    Task#728: pool=1
                                    Task#728 step 1/2 (+0s): 9.985µs self time
                                    Task#728 step 2/2 (+9.985µs): return nil
                                    Task#728 ends at 9.985µs
                                      Combine#728: index=3 flush=<nil>
                                      Combine#728 step 1/4 (+0s): 455ns self time
                                      Combine#728 step 2/4 (+455ns): scatter:
                                        Task#726: pool=2
                                        Task#726 step 1/2 (+0s): 10.002µs self time
                                        Task#726 step 2/2 (+10.002µs): return nil
                                        Task#726 ends at 20.442µs
                                          Gather#726: index=2
                                          Gather#726 step 1/12 (+0s): 246ns self time
                                          Gather#726 step 2/12 (+246ns): scatter:
                                            Task#723: pool=4
                                            Task#723 step 1/2 (+0s): 10.078µs self time
                                            Task#723 step 2/2 (+10.078µs): return nil
                                            Task#723 ends at 30.766µs
                                              Gather#723: index=3
                                              Gather#723 step 1/2 (+0s): 2.705µs self time
                                              Gather#723 step 2/2 (+2.705µs): return nil
                                              Gather#723 ends at 33.471µs
                                          Gather#726 step 3/12 (+246ns): 199ns self time
                                          Gather#726 step 4/12 (+445ns): scatter:
                                            Task#725: pool=3
                                            Task#725 step 1/2 (+0s): 9.999µs self time
                                            Task#725 step 2/2 (+9.999µs): return nil
                                            Task#725 ends at 30.886µs
                                              Combine#725: index=5 flush=<nil>
                                              Combine#725 step 1/6 (+0s): 370ns self time
                                              Combine#725 step 2/6 (+370ns): scatter:
                                                Task#717: pool=6
                                                Task#717 step 1/2 (+0s): 8.978µs self time
                                                Task#717 step 2/2 (+8.978µs): return nil
                                                Task#717 ends at 40.234µs
                                                  Combine#717: index=1 flush=<nil>
                                                  Combine#717 step 1/2 (+0s): 448.659µs self time
                                                  Combine#717 step 2/2 (+448.659µs): return nil
                                                  Combine#717 ends at 488.893µs
                                              Combine#725 step 3/6 (+370ns): 400ns self time
                                              Combine#725 step 4/6 (+770ns): scatter:
                                                Task#724: pool=4
                                                Task#724 step 1/2 (+0s): 9.986µs self time
                                                Task#724 step 2/2 (+9.986µs): return nil
                                                Task#724 ends at 41.642µs
                                                  Combine#724: index=2 flush=<nil>
                                                  Combine#724 step 1/4 (+0s): 525ns self time
                                                  Combine#724 step 2/4 (+525ns): scatter:
                                                    Task#714: pool=0
                                                    Task#714 step 1/2 (+0s): 0s self time
                                                    Task#714 step 2/2 (+0s): return nil
                                                    Task#714 ends at 42.167µs
                                                      Gather#714: index=4
                                                      Gather#714 step 1/2 (+0s): 996ns self time
                                                      Gather#714 step 2/2 (+996ns): return nil
                                                      Gather#714 ends at 43.163µs
                                                  Combine#724 step 3/4 (+525ns): 471ns self time
                                                  Combine#724 step 4/4 (+996ns): return nil
                                                  Combine#724 ends at 42.638µs
                                              Combine#725 step 5/6 (+770ns): 171ns self time
                                              Combine#725 step 6/6 (+941ns): return nil
                                              Combine#725 ends at 31.827µs
                                          Gather#726 step 5/12 (+445ns): 151ns self time
                                          Gather#726 step 6/12 (+596ns): scatter:
                                            Task#720: pool=2
                                            Task#720 step 1/2 (+0s): 10.001µs self time
                                            Task#720 step 2/2 (+10.001µs): return nil
                                            Task#720 ends at 31.039µs
                                              Gather#720: index=3
                                              Gather#720 step 1/2 (+0s): 1.151µs self time
                                              Gather#720 step 2/2 (+1.151µs): return nil
                                              Gather#720 ends at 32.19µs
                                          Gather#726 step 7/12 (+596ns): 375ns self time
                                          Gather#726 step 8/12 (+971ns): scatter:
                                            Task#721: pool=3
                                            Task#721 step 1/2 (+0s): 10.001µs self time
                                            Task#721 step 2/2 (+10.001µs): return nil
                                            Task#721 ends at 31.414µs
                                              Gather#721: index=3
                                              Gather#721 step 1/2 (+0s): 997ns self time
                                              Gather#721 step 2/2 (+997ns): return nil
                                              Gather#721 ends at 32.411µs
                                          Gather#726 step 9/12 (+971ns): 15ns self time
                                          Gather#726 step 10/12 (+986ns): scatter:
                                            Task#719: pool=3
                                            Task#719 step 1/2 (+0s): 26.015µs self time
                                            Task#719 step 2/2 (+26.015µs): return nil
                                            Task#719 ends at 47.443µs
                                              Gather#719: index=5
                                              Gather#719 step 1/2 (+0s): 999ns self time
                                              Gather#719 step 2/2 (+999ns): return nil
                                              Gather#719 ends at 48.442µs
                                          Gather#726 step 11/12 (+986ns): 84ns self time
                                          Gather#726 step 12/12 (+1.07µs): return error
                                          Gather#726 ends at 21.512µs
                                      Combine#728 step 3/4 (+455ns): 459ns self time
                                      Combine#728 step 4/4 (+914ns): return nil
                                      Combine#728 ends at 10.899µs
                                  Plan#27 step 3/4 (+0s): scatter:
                                    Task#729: pool=2
                                    Task#729 step 1/2 (+0s): 11.364µs self time
                                    Task#729 step 2/2 (+11.364µs): return nil
                                    Task#729 ends at 11.364µs
                                      Combine#729: index=4 flush=Gather#729
                                      Combine#729 step 1/4 (+0s): 198ns self time
                                      Combine#729 step 2/4 (+198ns): scatter:
                                        Task#718: pool=3
                                        Task#718 step 1/2 (+0s): 9.999µs self time
                                        Task#718 step 2/2 (+9.999µs): return nil
                                        Task#718 ends at 21.561µs
                                          Gather#718: index=2
                                          Gather#718 step 1/2 (+0s): 848.317µs self time
                                          Gather#718 step 2/2 (+848.317µs): return nil
                                          Gather#718 ends at 869.878µs
                                      Combine#729 step 3/4 (+198ns): 401ns self time
                                      Combine#729 step 4/4 (+599ns): return nil
                                      Combine#729 ends at 11.963µs
                                        Gather#729: index=1
                                        Gather#729 step 1/6 (+0s): 714ns self time
                                        Gather#729 step 2/6 (+714ns): scatter:
                                          Task#727: pool=2
                                          Task#727 step 1/2 (+0s): 9.999µs self time
                                          Task#727 step 2/2 (+9.999µs): return nil
                                          Task#727 ends at 0s
                                            Gather#727: index=1
                                            Gather#727 step 1/4 (+0s): 531ns self time
                                            Gather#727 step 2/4 (+531ns): scatter:
                                              Task#722: pool=0
                                              Task#722 step 1/2 (+0s): 10µs self time
                                              Task#722 step 2/2 (+10µs): return nil
                                              Task#722 ends at 0s
                                                Combine#722: index=1 flush=<nil>
                                                Combine#722 step 1/2 (+0s): 969ns self time
                                                Combine#722 step 2/2 (+969ns): return nil
                                                Combine#722 ends at 0s
                                            Gather#727 step 3/4 (+531ns): 470ns self time
                                            Gather#727 step 4/4 (+1.001µs): return nil
                                            Gather#727 ends at 0s
                                        Gather#729 step 3/6 (+714ns): 253ns self time
                                        Gather#729 step 4/6 (+967ns): scatter:
                                          Task#716: pool=4
                                          Task#716 step 1/2 (+0s): 7.927µs self time
                                          Task#716 step 2/2 (+7.927µs): return nil
                                          Task#716 ends at 0s
                                            Gather#716: index=5
                                            Gather#716 step 1/2 (+0s): 1.007µs self time
                                            Gather#716 step 2/2 (+1.007µs): return nil
                                            Gather#716 ends at 0s
                                        Gather#729 step 5/6 (+967ns): 238ns self time
                                        Gather#729 step 6/6 (+1.205µs): return nil
                                        Gather#729 ends at 0s
                                  Plan#27 step 4/4 (+0s): ends at 869.878µs
                                Gather#713 step 3/4 (+869.886µs): 998ns self time
                                Gather#713 step 4/4 (+870.884µs): return nil
                                Gather#713 ends at 7.660635ms
                            Combine#894 step 7/16 (+180ns): 161ns self time
                            Combine#894 step 8/16 (+341ns): scatter:
                              Task#781: pool=0
                              Task#781 step 1/2 (+0s): 9.998µs self time
                              Task#781 step 2/2 (+9.998µs): return nil
                              Task#781 ends at 6.790149ms
                                Gather#781: index=2
                                Gather#781 step 1/2 (+0s): 40.817µs self time
                                Gather#781 step 2/2 (+40.817µs): return nil
                                Gather#781 ends at 6.830966ms
                            Combine#894 step 9/16 (+341ns): 163ns self time
                            Combine#894 step 10/16 (+504ns): scatter:
                              Task#780: pool=4
                              Task#780 step 1/2 (+0s): 0s self time
                              Task#780 step 2/2 (+0s): return nil
                              Task#780 ends at 6.780314ms
                                Gather#780: index=0
                                Gather#780 step 1/2 (+0s): 1.077µs self time
                                Gather#780 step 2/2 (+1.077µs): return nil
                                Gather#780 ends at 6.781391ms
                            Combine#894 step 11/16 (+504ns): 500ns self time
                            Combine#894 step 12/16 (+1.004µs): scatter:
                              Task#834: pool=1
                              Task#834 step 1/2 (+0s): 10.005µs self time
                              Task#834 step 2/2 (+10.005µs): return nil
                              Task#834 ends at 6.790819ms
                                Combine#834: index=0 flush=<nil>
                                Combine#834 step 1/2 (+0s): 998ns self time
                                Combine#834 step 2/2 (+998ns): return nil
                                Combine#834 ends at 6.791817ms
                            Combine#894 step 13/16 (+1.004µs): 0s self time
                            Combine#894 step 14/16 (+1.004µs): scatter:
                              Task#864: pool=1
                              Task#864 step 1/4 (+0s): 4.36691ms self time
                              Task#864 step 2/4 (+4.36691ms): subjob:
                                Plan#32: pathCount=19 taskCount=29 maxPathDuration=8.011406ms minGatherCount=19 maxGatherCount=69
                                   TaskPools[0]: TaskPool#100: limit=1
                                   CombinerPools[0]: CombinerPool#148: limit=4
                                   CombinerPools[1]: CombinerPool#149: limit=2
                                   CombinerPools[2]: CombinerPool#150: limit=10
                                   CombinerPools[3]: CombinerPool#151: limit=1
                                   Combiners[0]: pool=1
                                   Combiners[1]: pool=3
                                   Combiners[2]: pool=1
                                   Combiners[3]: pool=2
                                   Combiners[4]: pool=3
                                   Combiners[5]: pool=1
                                   Combiners[6]: pool=2
                                   Combiners[7]: pool=1
                                   Combiners[8]: pool=2
                                   Combiners[9]: pool=3
                                   Combiners[10]: pool=1
                                   Combiners[11]: pool=1
                                   Combiners[12]: pool=1
                                   Combiners[13]: pool=0
                                   Combiners[14]: pool=3
                                   Combiners[15]: pool=1
                                   Combiners[16]: pool=1
                                   Combiners[17]: pool=2
                                   Combiners[18]: pool=3
                                Plan#32 step 1/9 (+0s): scatter:
                                  Task#882: pool=0
                                  Task#882 step 1/2 (+0s): 10µs self time
                                  Task#882 step 2/2 (+10µs): return nil
                                  Task#882 ends at 10µs
                                    Combine#882: index=16 flush=<nil>
                                    Combine#882 step 1/2 (+0s): 998ns self time
                                    Combine#882 step 2/2 (+998ns): return nil
                                    Combine#882 ends at 10.998µs
                                Plan#32 step 2/9 (+0s): scatter:
                                  Task#892: pool=0
                                  Task#892 step 1/2 (+0s): 9.99µs self time
                                  Task#892 step 2/2 (+9.99µs): return nil
                                  Task#892 ends at 9.99µs
                                    Gather#892: index=8
                                    Gather#892 step 1/4 (+0s): 538ns self time
                                    Gather#892 step 2/4 (+538ns): scatter:
                                      Task#889: pool=0
                                      Task#889 step 1/2 (+0s): 9.999µs self time
                                      Task#889 step 2/2 (+9.999µs): return nil
                                      Task#889 ends at 20.527µs
                                        Combine#889: index=0 flush=<nil>
                                        Combine#889 step 1/4 (+0s): 499.875µs self time
                                        Combine#889 step 2/4 (+499.875µs): scatter:
                                          Task#886: pool=0
                                          Task#886 step 1/2 (+0s): 10.001µs self time
                                          Task#886 step 2/2 (+10.001µs): return nil
                                          Task#886 ends at 530.403µs
                                            Gather#886: index=2
                                            Gather#886 step 1/8 (+0s): 3.457µs self time
                                            Gather#886 step 2/8 (+3.457µs): scatter:
                                              Task#884: pool=0
                                              Task#884 step 1/2 (+0s): 9.988µs self time
                                              Task#884 step 2/2 (+9.988µs): return nil
                                              Task#884 ends at 543.848µs
                                                Gather#884: index=19
                                                Gather#884 step 1/4 (+0s): 468.08µs self time
                                                Gather#884 step 2/4 (+468.08µs): scatter:
                                                  Task#878: pool=0
                                                  Task#878 step 1/2 (+0s): 10.01µs self time
                                                  Task#878 step 2/2 (+10.01µs): return nil
                                                  Task#878 ends at 1.021938ms
                                                    Gather#878: index=6
                                                    Gather#878 step 1/2 (+0s): 1µs self time
                                                    Gather#878 step 2/2 (+1µs): return nil
                                                    Gather#878 ends at 1.022938ms
                                                Gather#884 step 3/4 (+468.08µs): 468.087µs self time
                                                Gather#884 step 4/4 (+936.167µs): return nil
                                                Gather#884 ends at 1.480015ms
                                            Gather#886 step 3/8 (+3.457µs): 3.437µs self time
                                            Gather#886 step 4/8 (+6.894µs): scatter:
                                              Task#875: pool=0
                                              Task#875 step 1/2 (+0s): 10.998µs self time
                                              Task#875 step 2/2 (+10.998µs): return nil
                                              Task#875 ends at 548.295µs
                                                Combine#875: index=3 flush=<nil>
                                                Combine#875 step 1/2 (+0s): 997ns self time
                                                Combine#875 step 2/2 (+997ns): return nil
                                                Combine#875 ends at 549.292µs
                                            Gather#886 step 5/8 (+6.894µs): 3.476µs self time
                                            Gather#886 step 6/8 (+10.37µs): scatter:
                                              Task#885: pool=0
                                              Task#885 step 1/2 (+0s): 32.761µs self time
                                              Task#885 step 2/2 (+32.761µs): return nil
                                              Task#885 ends at 573.534µs
                                                Gather#885: index=11
                                                Gather#885 step 1/10 (+0s): 655ns self time
                                                Gather#885 step 2/10 (+655ns): scatter:
                                                  Task#865: pool=0
                                                  Task#865 step 1/2 (+0s): 10.001µs self time
                                                  Task#865 step 2/2 (+10.001µs): return nil
                                                  Task#865 ends at 584.19µs
                                                    Gather#865: index=18
                                                    Gather#865 step 1/2 (+0s): 1.101µs self time
                                                    Gather#865 step 2/2 (+1.101µs): return nil
                                                    Gather#865 ends at 585.291µs
                                                Gather#885 step 3/10 (+655ns): 66ns self time
                                                Gather#885 step 4/10 (+721ns): scatter:
                                                  Task#872: pool=0
                                                  Task#872 step 1/2 (+0s): 21.071µs self time
                                                  Task#872 step 2/2 (+21.071µs): return nil
                                                  Task#872 ends at 595.326µs
                                                    Gather#872: index=0
                                                    Gather#872 step 1/2 (+0s): 956ns self time
                                                    Gather#872 step 2/2 (+956ns): return nil
                                                    Gather#872 ends at 596.282µs
                                                Gather#885 step 5/10 (+721ns): 93ns self time
                                                Gather#885 step 6/10 (+814ns): scatter:
                                                  Task#880: pool=0
                                                  Task#880 step 1/2 (+0s): 9.994µs self time
                                                  Task#880 step 2/2 (+9.994µs): return nil
                                                  Task#880 ends at 584.342µs
                                                    Combine#880: index=9 flush=<nil>
                                                    Combine#880 step 1/2 (+0s): 1µs self time
                                                    Combine#880 step 2/2 (+1µs): return nil
                                                    Combine#880 ends at 585.342µs
                                                Gather#885 step 7/10 (+814ns): 117ns self time
                                                Gather#885 step 8/10 (+931ns): scatter:
                                                  Task#874: pool=0
                                                  Task#874 step 1/2 (+0s): 9.986µs self time
                                                  Task#874 step 2/2 (+9.986µs): return nil
                                                  Task#874 ends at 584.451µs
                                                    Gather#874: index=12
                                                    Gather#874 step 1/2 (+0s): 998ns self time
                                                    Gather#874 step 2/2 (+998ns): return nil
                                                    Gather#874 ends at 585.449µs
                                                Gather#885 step 9/10 (+931ns): 5ns self time
                                                Gather#885 step 10/10 (+936ns): return nil
                                                Gather#885 ends at 574.47µs
                                            Gather#886 step 7/8 (+10.37µs): 3.462µs self time
                                            Gather#886 step 8/8 (+13.832µs): return nil
                                            Gather#886 ends at 544.235µs
                                        Combine#889 step 3/4 (+499.875µs): 500.125µs self time
                                        Combine#889 step 4/4 (+1ms): return nil
                                        Combine#889 ends at 1.020527ms
                                    Gather#892 step 3/4 (+538ns): 461ns self time
                                    Gather#892 step 4/4 (+999ns): return nil
                                    Gather#892 ends at 10.989µs
                                Plan#32 step 3/9 (+0s): scatter:
                                  Task#873: pool=0
                                  Task#873 step 1/2 (+0s): 9.993µs self time
                                  Task#873 step 2/2 (+9.993µs): return nil
                                  Task#873 ends at 9.993µs
                                    Gather#873: index=5
                                    Gather#873 step 1/2 (+0s): 916ns self time
                                    Gather#873 step 2/2 (+916ns): return nil
                                    Gather#873 ends at 10.909µs
                                Plan#32 step 4/9 (+0s): scatter:
                                  Task#879: pool=0
                                  Task#879 step 1/2 (+0s): 9.896µs self time
                                  Task#879 step 2/2 (+9.896µs): return nil
                                  Task#879 ends at 9.896µs
                                    Gather#879: index=6
                                    Gather#879 step 1/2 (+0s): 1.115µs self time
                                    Gather#879 step 2/2 (+1.115µs): return nil
                                    Gather#879 ends at 11.011µs
                                Plan#32 step 5/9 (+0s): scatter:
                                  Task#893: pool=0
                                  Task#893 step 1/2 (+0s): 9.503µs self time
                                  Task#893 step 2/2 (+9.503µs): return nil
                                  Task#893 ends at 9.503µs
                                    Combine#893: index=6 flush=<nil>
                                    Combine#893 step 1/4 (+0s): 896ns self time
                                    Combine#893 step 2/4 (+896ns): scatter:
                                      Task#888: pool=0
                                      Task#888 step 1/2 (+0s): 9.752µs self time
                                      Task#888 step 2/2 (+9.752µs): return nil
                                      Task#888 ends at 20.151µs
                                        Combine#888: index=6 flush=<nil>
                                        Combine#888 step 1/4 (+0s): 2.605µs self time
                                        Combine#888 step 2/4 (+2.605µs): scatter:
                                          Task#869: pool=0
                                          Task#869 step 1/2 (+0s): 7.29µs self time
                                          Task#869 step 2/2 (+7.29µs): return nil
                                          Task#869 ends at 30.046µs
                                            Combine#869: index=2 flush=<nil>
                                            Combine#869 step 1/2 (+0s): 79ns self time
                                            Combine#869 step 2/2 (+79ns): return nil
                                            Combine#869 ends at 30.125µs
                                        Combine#888 step 3/4 (+2.605µs): 2.583µs self time
                                        Combine#888 step 4/4 (+5.188µs): return nil
                                        Combine#888 ends at 25.339µs
                                    Combine#893 step 3/4 (+896ns): 102ns self time
                                    Combine#893 step 4/4 (+998ns): return nil
                                    Combine#893 ends at 10.501µs
                                Plan#32 step 6/9 (+0s): scatter:
                                  Task#883: pool=0
                                  Task#883 step 1/2 (+0s): 4.600604ms self time
                                  Task#883 step 2/2 (+4.600604ms): return nil
                                  Task#883 ends at 4.600604ms
                                    Gather#883: index=0
                                    Gather#883 step 1/2 (+0s): 1.008µs self time
                                    Gather#883 step 2/2 (+1.008µs): return nil
                                    Gather#883 ends at 4.601612ms
                                Plan#32 step 7/9 (+0s): scatter:
                                  Task#866: pool=0
                                  Task#866 step 1/2 (+0s): 9.999µs self time
                                  Task#866 step 2/2 (+9.999µs): return nil
                                  Task#866 ends at 9.999µs
                                    Gather#866: index=0
                                    Gather#866 step 1/2 (+0s): 1.054µs self time
                                    Gather#866 step 2/2 (+1.054µs): return nil
                                    Gather#866 ends at 11.053µs
                                Plan#32 step 8/9 (+0s): scatter:
                                  Task#891: pool=0
                                  Task#891 step 1/2 (+0s): 9.999µs self time
                                  Task#891 step 2/2 (+9.999µs): return nil
                                  Task#891 ends at 9.999µs
                                    Combine#891: index=6 flush=<nil>
                                    Combine#891 step 1/14 (+0s): 207ns self time
                                    Combine#891 step 2/14 (+207ns): scatter:
                                      Task#870: pool=0
                                      Task#870 step 1/2 (+0s): 8.000199ms self time
                                      Task#870 step 2/2 (+8.000199ms): return nil
                                      Task#870 ends at 8.010405ms
                                        Gather#870: index=0
                                        Gather#870 step 1/2 (+0s): 1.001µs self time
                                        Gather#870 step 2/2 (+1.001µs): return nil
                                        Gather#870 ends at 8.011406ms
                                    Combine#891 step 3/14 (+207ns): 206ns self time
                                    Combine#891 step 4/14 (+413ns): scatter:
                                      Task#876: pool=0
                                      Task#876 step 1/2 (+0s): 10.209µs self time
                                      Task#876 step 2/2 (+10.209µs): return nil
                                      Task#876 ends at 20.621µs
                                        Gather#876: index=18
                                        Gather#876 step 1/2 (+0s): 2.222µs self time
                                        Gather#876 step 2/2 (+2.222µs): return nil
                                        Gather#876 ends at 22.843µs
                                    Combine#891 step 5/14 (+413ns): 177ns self time
                                    Combine#891 step 6/14 (+590ns): scatter:
                                      Task#890: pool=0
                                      Task#890 step 1/2 (+0s): 0s self time
                                      Task#890 step 2/2 (+0s): return error
                                      Task#890 ends at 10.589µs
                                        Gather#890: index=6
                                        Gather#890 step 1/6 (+0s): 645ns self time
                                        Gather#890 step 2/6 (+645ns): scatter:
                                          Task#887: pool=0
                                          Task#887 step 1/2 (+0s): 9.988µs self time
                                          Task#887 step 2/2 (+9.988µs): return nil
                                          Task#887 ends at 21.222µs
                                            Combine#887: index=4 flush=<nil>
                                            Combine#887 step 1/4 (+0s): 321.985µs self time
                                            Combine#887 step 2/4 (+321.985µs): scatter:
                                              Task#868: pool=0
                                              Task#868 step 1/2 (+0s): 4.406µs self time
                                              Task#868 step 2/2 (+4.406µs): return error
                                              Task#868 ends at 347.613µs
                                                Gather#868: index=1
                                                Gather#868 step 1/2 (+0s): 863ns self time
                                                Gather#868 step 2/2 (+863ns): return nil
                                                Gather#868 ends at 348.476µs
                                            Combine#887 step 3/4 (+321.985µs): 321.963µs self time
                                            Combine#887 step 4/4 (+643.948µs): return nil
                                            Combine#887 ends at 665.17µs
                                        Gather#890 step 3/6 (+645ns): 47ns self time
                                        Gather#890 step 4/6 (+692ns): scatter:
                                          Task#881: pool=0
                                          Task#881 step 1/2 (+0s): 9.998µs self time
                                          Task#881 step 2/2 (+9.998µs): return nil
                                          Task#881 ends at 21.279µs
                                            Gather#881: index=4
                                            Gather#881 step 1/2 (+0s): 985ns self time
                                            Gather#881 step 2/2 (+985ns): return nil
                                            Gather#881 ends at 22.264µs
                                        Gather#890 step 5/6 (+692ns): 32ns self time
                                        Gather#890 step 6/6 (+724ns): return nil
                                        Gather#890 ends at 11.313µs
                                    Combine#891 step 7/14 (+590ns): 96ns self time
                                    Combine#891 step 8/14 (+686ns): scatter:
                                      Task#877: pool=0
                                      Task#877 step 1/2 (+0s): 9.761µs self time
                                      Task#877 step 2/2 (+9.761µs): return nil
                                      Task#877 ends at 20.446µs
                                        Combine#877: index=7 flush=<nil>
                                        Combine#877 step 1/2 (+0s): 3.331µs self time
                                        Combine#877 step 2/2 (+3.331µs): return nil
                                        Combine#877 ends at 23.777µs
                                    Combine#891 step 9/14 (+686ns): 395ns self time
                                    Combine#891 step 10/14 (+1.081µs): scatter:
                                      Task#867: pool=0
                                      Task#867 step 1/2 (+0s): 203.125µs self time
                                      Task#867 step 2/2 (+203.125µs): return nil
                                      Task#867 ends at 214.205µs
                                        Gather#867: index=2
                                        Gather#867 step 1/2 (+0s): 999ns self time
                                        Gather#867 step 2/2 (+999ns): return nil
                                        Gather#867 ends at 215.204µs
                                    Combine#891 step 11/14 (+1.081µs): 2ns self time
                                    Combine#891 step 12/14 (+1.083µs): scatter:
                                      Task#871: pool=0
                                      Task#871 step 1/2 (+0s): 9.994µs self time
                                      Task#871 step 2/2 (+9.994µs): return nil
                                      Task#871 ends at 21.076µs
                                        Gather#871: index=12
                                        Gather#871 step 1/2 (+0s): 994ns self time
                                        Gather#871 step 2/2 (+994ns): return nil
                                        Gather#871 ends at 22.07µs
                                    Combine#891 step 13/14 (+1.083µs): 359ns self time
                                    Combine#891 step 14/14 (+1.442µs): return nil
                                    Combine#891 ends at 11.441µs
                                Plan#32 step 9/9 (+0s): ends at 8.011406ms
                              Task#864 step 3/4 (+12.378316ms): 4.366909ms self time
                              Task#864 step 4/4 (+16.745225ms): return nil
                              Task#864 ends at 23.526039ms
                                Combine#864: index=0 flush=<nil>
                                Combine#864 step 1/14 (+0s): 87ns self time
                                Combine#864 step 2/14 (+87ns): scatter:
                                  Task#862: pool=0
                                  Task#862 step 1/2 (+0s): 9.997µs self time
                                  Task#862 step 2/2 (+9.997µs): return nil
                                  Task#862 ends at 23.536123ms
                                    Combine#862: index=0 flush=<nil>
                                    Combine#862 step 1/4 (+0s): 22ns self time
                                    Combine#862 step 2/4 (+22ns): scatter:
                                      Task#711: pool=4
                                      Task#711 step 1/2 (+0s): 10.018µs self time
                                      Task#711 step 2/2 (+10.018µs): return nil
                                      Task#711 ends at 23.546163ms
                                        Gather#711: index=5
                                        Gather#711 step 1/2 (+0s): 1µs self time
                                        Gather#711 step 2/2 (+1µs): return nil
                                        Gather#711 ends at 23.547163ms
                                    Combine#862 step 3/4 (+22ns): 983ns self time
                                    Combine#862 step 4/4 (+1.005µs): return nil
                                    Combine#862 ends at 23.537128ms
                                Combine#864 step 3/14 (+87ns): 161ns self time
                                Combine#864 step 4/14 (+248ns): scatter:
                                  Task#861: pool=1
                                  Task#861 step 1/2 (+0s): 10µs self time
                                  Task#861 step 2/2 (+10µs): return nil
                                  Task#861 ends at 23.536287ms
                                    Gather#861: index=0
                                    Gather#861 step 1/4 (+0s): 511ns self time
                                    Gather#861 step 2/4 (+511ns): scatter:
                                      Task#710: pool=0
                                      Task#710 step 1/2 (+0s): 4.770075ms self time
                                      Task#710 step 2/2 (+4.770075ms): return nil
                                      Task#710 ends at 28.306873ms
                                        Gather#710: index=0
                                        Gather#710 step 1/2 (+0s): 518ns self time
                                        Gather#710 step 2/2 (+518ns): return nil
                                        Gather#710 ends at 28.307391ms
                                    Gather#861 step 3/4 (+511ns): 490ns self time
                                    Gather#861 step 4/4 (+1.001µs): return nil
                                    Gather#861 ends at 23.537288ms
                                Combine#864 step 5/14 (+248ns): 122ns self time
                                Combine#864 step 6/14 (+370ns): scatter:
                                  Task#707: pool=1
                                  Task#707 step 1/2 (+0s): 8.859µs self time
                                  Task#707 step 2/2 (+8.859µs): return nil
                                  Task#707 ends at 23.535268ms
                                    Combine#707: index=0 flush=<nil>
                                    Combine#707 step 1/2 (+0s): 17ns self time
                                    Combine#707 step 2/2 (+17ns): return nil
                                    Combine#707 ends at 23.535285ms
                                Combine#864 step 7/14 (+370ns): 486ns self time
                                Combine#864 step 8/14 (+856ns): scatter:
                                  Task#860: pool=0
                                  Task#860 step 1/2 (+0s): 9.997µs self time
                                  Task#860 step 2/2 (+9.997µs): return nil
                                  Task#860 ends at 23.536892ms
                                    Gather#860: index=4
                                    Gather#860 step 1/6 (+0s): 311ns self time
                                    Gather#860 step 2/6 (+311ns): scatter:
                                      Task#730: pool=2
                                      Task#730 step 1/2 (+0s): 18.272µs self time
                                      Task#730 step 2/2 (+18.272µs): return nil
                                      Task#730 ends at 23.555475ms
                                        Gather#730: index=0
                                        Gather#730 step 1/2 (+0s): 1.398µs self time
                                        Gather#730 step 2/2 (+1.398µs): return nil
                                        Gather#730 ends at 23.556873ms
                                    Gather#860 step 3/6 (+311ns): 335ns self time
                                    Gather#860 step 4/6 (+646ns): scatter:
                                      Task#779: pool=1
                                      Task#779 step 1/2 (+0s): 9.998µs self time
                                      Task#779 step 2/2 (+9.998µs): return nil
                                      Task#779 ends at 23.547536ms
                                        Gather#779: index=4
                                        Gather#779 step 1/2 (+0s): 673.798µs self time
                                        Gather#779 step 2/2 (+673.798µs): return error
                                        Gather#779 ends at 24.221334ms
                                    Gather#860 step 5/6 (+646ns): 335ns self time
                                    Gather#860 step 6/6 (+981ns): return nil
                                    Gather#860 ends at 23.537873ms
                                Combine#864 step 9/14 (+856ns): 68ns self time
                                Combine#864 step 10/14 (+924ns): scatter:
                                  Task#732: pool=1
                                  Task#732 step 1/4 (+0s): 0s self time
                                  Task#732 step 2/4 (+0s): subjob:
                                    Plan#28: pathCount=25 taskCount=46 maxPathDuration=454.078µs minGatherCount=40 maxGatherCount=64
                                       TaskPools[0]: TaskPool#86: limit=6
                                       CombinerPools[0]: CombinerPool#136: limit=4
                                       Combiners[0]: pool=0
                                       Combiners[1]: pool=0
                                       Combiners[2]: pool=0
                                       Combiners[3]: pool=0
                                       Combiners[4]: pool=0
                                       Combiners[5]: pool=0
                                       Combiners[6]: pool=0
                                       Combiners[7]: pool=0
                                       Combiners[8]: pool=0
                                       Combiners[9]: pool=0
                                       Combiners[10]: pool=0
                                       Combiners[11]: pool=0
                                       Combiners[12]: pool=0
                                       Combiners[13]: pool=0
                                       Combiners[14]: pool=0
                                       Combiners[15]: pool=0
                                       Combiners[16]: pool=0
                                       Combiners[17]: pool=0
                                    Plan#28 step 1/11 (+0s): scatter:
                                      Task#775: pool=0
                                      Task#775 step 1/2 (+0s): 9.996µs self time
                                      Task#775 step 2/2 (+9.996µs): return nil
                                      Task#775 ends at 9.996µs
                                        Gather#775: index=1
                                        Gather#775 step 1/8 (+0s): 289ns self time
                                        Gather#775 step 2/8 (+289ns): scatter:
                                          Task#766: pool=0
                                          Task#766 step 1/2 (+0s): 9.975µs self time
                                          Task#766 step 2/2 (+9.975µs): return nil
                                          Task#766 ends at 20.26µs
                                            Combine#766: index=1 flush=<nil>
                                            Combine#766 step 1/4 (+0s): 154ns self time
                                            Combine#766 step 2/4 (+154ns): scatter:
                                              Task#734: pool=0
                                              Task#734 step 1/2 (+0s): 11.314µs self time
                                              Task#734 step 2/2 (+11.314µs): return nil
                                              Task#734 ends at 31.728µs
                                                Gather#734: index=0
                                                Gather#734 step 1/2 (+0s): 8.504µs self time
                                                Gather#734 step 2/2 (+8.504µs): return nil
                                                Gather#734 ends at 40.232µs
                                            Combine#766 step 3/4 (+154ns): 155ns self time
                                            Combine#766 step 4/4 (+309ns): return nil
                                            Combine#766 ends at 20.569µs
                                        Gather#775 step 3/8 (+289ns): 299ns self time
                                        Gather#775 step 4/8 (+588ns): scatter:
                                          Task#769: pool=0
                                          Task#769 step 1/2 (+0s): 21.496µs self time
                                          Task#769 step 2/2 (+21.496µs): return nil
                                          Task#769 ends at 32.08µs
                                            Combine#769: index=0 flush=<nil>
                                            Combine#769 step 1/4 (+0s): 39.8µs self time
                                            Combine#769 step 2/4 (+39.8µs): scatter:
                                              Task#744: pool=0
                                              Task#744 step 1/2 (+0s): 9.999µs self time
                                              Task#744 step 2/2 (+9.999µs): return error
                                              Task#744 ends at 81.879µs
                                                Gather#744: index=4
                                                Gather#744 step 1/2 (+0s): 1.004µs self time
                                                Gather#744 step 2/2 (+1.004µs): return nil
                                                Gather#744 ends at 82.883µs
                                            Combine#769 step 3/4 (+39.8µs): 38.972µs self time
                                            Combine#769 step 4/4 (+78.772µs): return nil
                                            Combine#769 ends at 110.852µs
                                        Gather#775 step 5/8 (+588ns): 354ns self time
                                        Gather#775 step 6/8 (+942ns): scatter:
                                          Task#741: pool=0
                                          Task#741 step 1/2 (+0s): 9.932µs self time
                                          Task#741 step 2/2 (+9.932µs): return nil
                                          Task#741 ends at 20.87µs
                                            Gather#741: index=4
                                            Gather#741 step 1/2 (+0s): 995ns self time
                                            Gather#741 step 2/2 (+995ns): return nil
                                            Gather#741 ends at 21.865µs
                                        Gather#775 step 7/8 (+942ns): 243ns self time
                                        Gather#775 step 8/8 (+1.185µs): return nil
                                        Gather#775 ends at 11.181µs
                                    Plan#28 step 2/11 (+0s): scatter:
                                      Task#773: pool=0
                                      Task#773 step 1/2 (+0s): 10.121µs self time
                                      Task#773 step 2/2 (+10.121µs): return nil
                                      Task#773 ends at 10.121µs
                                        Gather#773: index=0
                                        Gather#773 step 1/10 (+0s): 630ns self time
                                        Gather#773 step 2/10 (+630ns): scatter:
                                          Task#755: pool=0
                                          Task#755 step 1/2 (+0s): 9.997µs self time
                                          Task#755 step 2/2 (+9.997µs): return nil
                                          Task#755 ends at 20.748µs
                                            Gather#755: index=2
                                            Gather#755 step 1/2 (+0s): 882ns self time
                                            Gather#755 step 2/2 (+882ns): return nil
                                            Gather#755 ends at 21.63µs
                                        Gather#773 step 3/10 (+630ns): 49ns self time
                                        Gather#773 step 4/10 (+679ns): scatter:
                                          Task#768: pool=0
                                          Task#768 step 1/2 (+0s): 9.985µs self time
                                          Task#768 step 2/2 (+9.985µs): return nil
                                          Task#768 ends at 20.785µs
                                            Gather#768: index=7
                                            Gather#768 step 1/4 (+0s): 513ns self time
                                            Gather#768 step 2/4 (+513ns): scatter:
                                              Task#733: pool=0
                                              Task#733 step 1/2 (+0s): 9.858µs self time
                                              Task#733 step 2/2 (+9.858µs): return nil
                                              Task#733 ends at 31.156µs
                                                Gather#733: index=0
                                                Gather#733 step 1/2 (+0s): 967ns self time
                                                Gather#733 step 2/2 (+967ns): return nil
                                                Gather#733 ends at 32.123µs
                                            Gather#768 step 3/4 (+513ns): 486ns self time
                                            Gather#768 step 4/4 (+999ns): return nil
                                            Gather#768 ends at 21.784µs
                                        Gather#773 step 5/10 (+679ns): 4ns self time
                                        Gather#773 step 6/10 (+683ns): scatter:
                                          Task#767: pool=0
                                          Task#767 step 1/2 (+0s): 10.022µs self time
                                          Task#767 step 2/2 (+10.022µs): return error
                                          Task#767 ends at 20.826µs
                                            Gather#767: index=9
                                            Gather#767 step 1/4 (+0s): 368ns self time
                                            Gather#767 step 2/4 (+368ns): scatter:
                                              Task#764: pool=0
                                              Task#764 step 1/2 (+0s): 11.775µs self time
                                              Task#764 step 2/2 (+11.775µs): return nil
                                              Task#764 ends at 32.969µs
                                                Gather#764: index=7
                                                Gather#764 step 1/10 (+0s): 130ns self time
                                                Gather#764 step 2/10 (+130ns): scatter:
                                                  Task#738: pool=0
                                                  Task#738 step 1/2 (+0s): 10µs self time
                                                  Task#738 step 2/2 (+10µs): return nil
                                                  Task#738 ends at 43.099µs
                                                    Gather#738: index=14
                                                    Gather#738 step 1/2 (+0s): 1.007µs self time
                                                    Gather#738 step 2/2 (+1.007µs): return error
                                                    Gather#738 ends at 44.106µs
                                                Gather#764 step 3/10 (+130ns): 116ns self time
                                                Gather#764 step 4/10 (+246ns): scatter:
                                                  Task#746: pool=0
                                                  Task#746 step 1/2 (+0s): 10.001µs self time
                                                  Task#746 step 2/2 (+10.001µs): return nil
                                                  Task#746 ends at 43.216µs
                                                    Gather#746: index=11
                                                    Gather#746 step 1/2 (+0s): 998ns self time
                                                    Gather#746 step 2/2 (+998ns): return nil
                                                    Gather#746 ends at 44.214µs
                                                Gather#764 step 5/10 (+246ns): 20ns self time
                                                Gather#764 step 6/10 (+266ns): scatter:
                                                  Task#737: pool=0
                                                  Task#737 step 1/2 (+0s): 9.999µs self time
                                                  Task#737 step 2/2 (+9.999µs): return nil
                                                  Task#737 ends at 43.234µs
                                                    Gather#737: index=0
                                                    Gather#737 step 1/2 (+0s): 1.009µs self time
                                                    Gather#737 step 2/2 (+1.009µs): return nil
                                                    Gather#737 ends at 44.243µs
                                                Gather#764 step 7/10 (+266ns): 172ns self time
                                                Gather#764 step 8/10 (+438ns): scatter:
                                                  Task#758: pool=0
                                                  Task#758 step 1/2 (+0s): 51.82µs self time
                                                  Task#758 step 2/2 (+51.82µs): return nil
                                                  Task#758 ends at 85.227µs
                                                    Combine#758: index=13 flush=<nil>
                                                    Combine#758 step 1/4 (+0s): 683ns self time
                                                    Combine#758 step 2/4 (+683ns): scatter:
                                                      Task#739: pool=0
                                                      Task#739 step 1/2 (+0s): 10.009µs self time
                                                      Task#739 step 2/2 (+10.009µs): return nil
                                                      Task#739 ends at 95.919µs
                                                        Gather#739: index=1
                                                        Gather#739 step 1/2 (+0s): 851ns self time
                                                        Gather#739 step 2/2 (+851ns): return nil
                                                        Gather#739 ends at 96.77µs
                                                    Combine#758 step 3/4 (+683ns): 176ns self time
                                                    Combine#758 step 4/4 (+859ns): return nil
                                                    Combine#758 ends at 86.086µs
                                                Gather#764 step 9/10 (+438ns): 152ns self time
                                                Gather#764 step 10/10 (+590ns): return nil
                                                Gather#764 ends at 33.559µs
                                            Gather#767 step 3/4 (+368ns): 589ns self time
                                            Gather#767 step 4/4 (+957ns): return nil
                                            Gather#767 ends at 21.783µs
                                        Gather#773 step 7/10 (+683ns): 78ns self time
                                        Gather#773 step 8/10 (+761ns): scatter:
                                          Task#750: pool=0
                                          Task#750 step 1/2 (+0s): 10.888µs self time
                                          Task#750 step 2/2 (+10.888µs): return nil
                                          Task#750 ends at 21.77µs
                                            Gather#750: index=2
                                            Gather#750 step 1/2 (+0s): 998ns self time
                                            Gather#750 step 2/2 (+998ns): return nil
                                            Gather#750 ends at 22.768µs
                                        Gather#773 step 9/10 (+761ns): 242ns self time
                                        Gather#773 step 10/10 (+1.003µs): return nil
                                        Gather#773 ends at 11.124µs
                                    Plan#28 step 3/11 (+0s): scatter:
                                      Task#778: pool=0
                                      Task#778 step 1/2 (+0s): 9.998µs self time
                                      Task#778 step 2/2 (+9.998µs): return nil
                                      Task#778 ends at 9.998µs
                                        Gather#778: index=1
                                        Gather#778 step 1/8 (+0s): 213ns self time
                                        Gather#778 step 2/8 (+213ns): scatter:
                                          Task#771: pool=0
                                          Task#771 step 1/2 (+0s): 9.288µs self time
                                          Task#771 step 2/2 (+9.288µs): return nil
                                          Task#771 ends at 19.499µs
                                            Gather#771: index=7
                                            Gather#771 step 1/8 (+0s): 239ns self time
                                            Gather#771 step 2/8 (+239ns): scatter:
                                              Task#761: pool=0
                                              Task#761 step 1/2 (+0s): 9.986µs self time
                                              Task#761 step 2/2 (+9.986µs): return nil
                                              Task#761 ends at 29.724µs
                                                Combine#761: index=3 flush=Gather#761
                                                Combine#761 step 1/2 (+0s): 994ns self time
                                                Combine#761 step 2/2 (+994ns): return nil
                                                Combine#761 ends at 30.718µs
                                                  Gather#761: index=11
                                                  Gather#761 step 1/4 (+0s): 628ns self time
                                                  Gather#761 step 2/4 (+628ns): scatter:
                                                    Task#754: pool=0
                                                    Task#754 step 1/2 (+0s): 10.066µs self time
                                                    Task#754 step 2/2 (+10.066µs): return nil
                                                    Task#754 ends at 0s
                                                      Gather#754: index=14
                                                      Gather#754 step 1/2 (+0s): 391ns self time
                                                      Gather#754 step 2/2 (+391ns): return nil
                                                      Gather#754 ends at 0s
                                                  Gather#761 step 3/4 (+628ns): 366ns self time
                                                  Gather#761 step 4/4 (+994ns): return nil
                                                  Gather#761 ends at 0s
                                            Gather#771 step 3/8 (+239ns): 248ns self time
                                            Gather#771 step 4/8 (+487ns): scatter:
                                              Task#762: pool=0
                                              Task#762 step 1/2 (+0s): 9.999µs self time
                                              Task#762 step 2/2 (+9.999µs): return nil
                                              Task#762 ends at 29.985µs
                                                Gather#762: index=3
                                                Gather#762 step 1/4 (+0s): 658ns self time
                                                Gather#762 step 2/4 (+658ns): scatter:
                                                  Task#760: pool=0
                                                  Task#760 step 1/2 (+0s): 10.014µs self time
                                                  Task#760 step 2/2 (+10.014µs): return nil
                                                  Task#760 ends at 40.657µs
                                                    Combine#760: index=6 flush=<nil>
                                                    Combine#760 step 1/4 (+0s): 495ns self time
                                                    Combine#760 step 2/4 (+495ns): scatter:
                                                      Task#745: pool=0
                                                      Task#745 step 1/2 (+0s): 9.998µs self time
                                                      Task#745 step 2/2 (+9.998µs): return nil
                                                      Task#745 ends at 51.15µs
                                                        Gather#745: index=4
                                                        Gather#745 step 1/2 (+0s): 821ns self time
                                                        Gather#745 step 2/2 (+821ns): return nil
                                                        Gather#745 ends at 51.971µs
                                                    Combine#760 step 3/4 (+495ns): 495ns self time
                                                    Combine#760 step 4/4 (+990ns): return nil
                                                    Combine#760 ends at 41.647µs
                                                Gather#762 step 3/4 (+658ns): 701ns self time
                                                Gather#762 step 4/4 (+1.359µs): return nil
                                                Gather#762 ends at 31.344µs
                                            Gather#771 step 5/8 (+487ns): 254ns self time
                                            Gather#771 step 6/8 (+741ns): scatter:
                                              Task#748: pool=0
                                              Task#748 step 1/2 (+0s): 0s self time
                                              Task#748 step 2/2 (+0s): return nil
                                              Task#748 ends at 20.24µs
                                                Gather#748: index=14
                                                Gather#748 step 1/2 (+0s): 1.268µs self time
                                                Gather#748 step 2/2 (+1.268µs): return nil
                                                Gather#748 ends at 21.508µs
                                            Gather#771 step 7/8 (+741ns): 241ns self time
                                            Gather#771 step 8/8 (+982ns): return nil
                                            Gather#771 ends at 20.481µs
                                        Gather#778 step 3/8 (+213ns): 108ns self time
                                        Gather#778 step 4/8 (+321ns): scatter:
                                          Task#770: pool=0
                                          Task#770 step 1/2 (+0s): 13.321µs self time
                                          Task#770 step 2/2 (+13.321µs): return nil
                                          Task#770 ends at 23.64µs
                                            Gather#770: index=0
                                            Gather#770 step 1/4 (+0s): 3.814µs self time
                                            Gather#770 step 2/4 (+3.814µs): scatter:
                                              Task#763: pool=0
                                              Task#763 step 1/2 (+0s): 9.984µs self time
                                              Task#763 step 2/2 (+9.984µs): return nil
                                              Task#763 ends at 37.438µs
                                                Gather#763: index=1
                                                Gather#763 step 1/4 (+0s): 0s self time
                                                Gather#763 step 2/4 (+0s): scatter:
                                                  Task#736: pool=0
                                                  Task#736 step 1/2 (+0s): 10.017µs self time
                                                  Task#736 step 2/2 (+10.017µs): return nil
                                                  Task#736 ends at 47.455µs
                                                    Gather#736: index=11
                                                    Gather#736 step 1/2 (+0s): 977ns self time
                                                    Gather#736 step 2/2 (+977ns): return nil
                                                    Gather#736 ends at 48.432µs
                                                Gather#763 step 3/4 (+0s): 0s self time
                                                Gather#763 step 4/4 (+0s): return nil
                                                Gather#763 ends at 37.438µs
                                            Gather#770 step 3/4 (+3.814µs): 3.881µs self time
                                            Gather#770 step 4/4 (+7.695µs): return nil
                                            Gather#770 ends at 31.335µs
                                        Gather#778 step 5/8 (+321ns): 330ns self time
                                        Gather#778 step 6/8 (+651ns): scatter:
                                          Task#742: pool=0
                                          Task#742 step 1/2 (+0s): 8.32µs self time
                                          Task#742 step 2/2 (+8.32µs): return nil
                                          Task#742 ends at 18.969µs
                                            Gather#742: index=2
                                            Gather#742 step 1/2 (+0s): 808ns self time
                                            Gather#742 step 2/2 (+808ns): return error
                                            Gather#742 ends at 19.777µs
                                        Gather#778 step 7/8 (+651ns): 347ns self time
                                        Gather#778 step 8/8 (+998ns): return nil
                                        Gather#778 ends at 10.996µs
                                    Plan#28 step 4/11 (+0s): scatter:
                                      Task#774: pool=0
                                      Task#774 step 1/2 (+0s): 9.977µs self time
                                      Task#774 step 2/2 (+9.977µs): return nil
                                      Task#774 ends at 9.977µs
                                        Gather#774: index=5
                                        Gather#774 step 1/4 (+0s): 515ns self time
                                        Gather#774 step 2/4 (+515ns): scatter:
                                          Task#756: pool=0
                                          Task#756 step 1/2 (+0s): 10.035µs self time
                                          Task#756 step 2/2 (+10.035µs): return nil
                                          Task#756 ends at 20.527µs
                                            Combine#756: index=1 flush=<nil>
                                            Combine#756 step 1/2 (+0s): 1.002µs self time
                                            Combine#756 step 2/2 (+1.002µs): return nil
                                            Combine#756 ends at 21.529µs
                                        Gather#774 step 3/4 (+515ns): 596ns self time
                                        Gather#774 step 4/4 (+1.111µs): return nil
                                        Gather#774 ends at 11.088µs
                                    Plan#28 step 5/11 (+0s): scatter:
                                      Task#743: pool=0
                                      Task#743 step 1/2 (+0s): 453.068µs self time
                                      Task#743 step 2/2 (+453.068µs): return nil
                                      Task#743 ends at 453.068µs
                                        Gather#743: index=1
                                        Gather#743 step 1/2 (+0s): 1.01µs self time
                                        Gather#743 step 2/2 (+1.01µs): return nil
                                        Gather#743 ends at 454.078µs
                                    Plan#28 step 6/11 (+0s): scatter:
                                      Task#776: pool=0
                                      Task#776 step 1/2 (+0s): 2.113µs self time
                                      Task#776 step 2/2 (+2.113µs): return nil
                                      Task#776 ends at 2.113µs
                                        Gather#776: index=0
                                        Gather#776 step 1/4 (+0s): 644ns self time
                                        Gather#776 step 2/4 (+644ns): scatter:
                                          Task#757: pool=0
                                          Task#757 step 1/2 (+0s): 9.996µs self time
                                          Task#757 step 2/2 (+9.996µs): return nil
                                          Task#757 ends at 12.753µs
                                            Gather#757: index=12
                                            Gather#757 step 1/2 (+0s): 895ns self time
                                            Gather#757 step 2/2 (+895ns): return nil
                                            Gather#757 ends at 13.648µs
                                        Gather#776 step 3/4 (+644ns): 353ns self time
                                        Gather#776 step 4/4 (+997ns): return nil
                                        Gather#776 ends at 3.11µs
                                    Plan#28 step 7/11 (+0s): scatter:
                                      Task#735: pool=0
                                      Task#735 step 1/2 (+0s): 9.942µs self time
                                      Task#735 step 2/2 (+9.942µs): return nil
                                      Task#735 ends at 9.942µs
                                        Gather#735: index=1
                                        Gather#735 step 1/2 (+0s): 989ns self time
                                        Gather#735 step 2/2 (+989ns): return nil
                                        Gather#735 ends at 10.931µs
                                    Plan#28 step 8/11 (+0s): scatter:
                                      Task#753: pool=0
                                      Task#753 step 1/2 (+0s): 12.59µs self time
                                      Task#753 step 2/2 (+12.59µs): return nil
                                      Task#753 ends at 12.59µs
                                        Gather#753: index=0
                                        Gather#753 step 1/2 (+0s): 25.185µs self time
                                        Gather#753 step 2/2 (+25.185µs): return nil
                                        Gather#753 ends at 37.775µs
                                    Plan#28 step 9/11 (+0s): scatter:
                                      Task#777: pool=0
                                      Task#777 step 1/2 (+0s): 9.961µs self time
                                      Task#777 step 2/2 (+9.961µs): return nil
                                      Task#777 ends at 9.961µs
                                        Combine#777: index=2 flush=<nil>
                                        Combine#777 step 1/4 (+0s): 13.311µs self time
                                        Combine#777 step 2/4 (+13.311µs): scatter:
                                          Task#772: pool=0
                                          Task#772 step 1/2 (+0s): 9.999µs self time
                                          Task#772 step 2/2 (+9.999µs): return nil
                                          Task#772 ends at 33.271µs
                                            Gather#772: index=0
                                            Gather#772 step 1/6 (+0s): 253ns self time
                                            Gather#772 step 2/6 (+253ns): scatter:
                                              Task#752: pool=0
                                              Task#752 step 1/2 (+0s): 10.194µs self time
                                              Task#752 step 2/2 (+10.194µs): return nil
                                              Task#752 ends at 43.718µs
                                                Gather#752: index=9
                                                Gather#752 step 1/2 (+0s): 796ns self time
                                                Gather#752 step 2/2 (+796ns): return nil
                                                Gather#752 ends at 44.514µs
                                            Gather#772 step 3/6 (+253ns): 41ns self time
                                            Gather#772 step 4/6 (+294ns): scatter:
                                              Task#765: pool=0
                                              Task#765 step 1/2 (+0s): 10.007µs self time
                                              Task#765 step 2/2 (+10.007µs): return nil
                                              Task#765 ends at 43.572µs
                                                Gather#765: index=3
                                                Gather#765 step 1/8 (+0s): 75ns self time
                                                Gather#765 step 2/8 (+75ns): scatter:
                                                  Task#759: pool=0
                                                  Task#759 step 1/2 (+0s): 9.978µs self time
                                                  Task#759 step 2/2 (+9.978µs): return nil
                                                  Task#759 ends at 53.625µs
                                                    Gather#759: index=6
                                                    Gather#759 step 1/4 (+0s): 498ns self time
                                                    Gather#759 step 2/4 (+498ns): scatter:
                                                      Task#740: pool=0
                                                      Task#740 step 1/2 (+0s): 9.994µs self time
                                                      Task#740 step 2/2 (+9.994µs): return nil
                                                      Task#740 ends at 64.117µs
                                                        Gather#740: index=9
                                                        Gather#740 step 1/2 (+0s): 0s self time
                                                        Gather#740 step 2/2 (+0s): return nil
                                                        Gather#740 ends at 64.117µs
                                                    Gather#759 step 3/4 (+498ns): 501ns self time
                                                    Gather#759 step 4/4 (+999ns): return nil
                                                    Gather#759 ends at 54.624µs
                                                Gather#765 step 3/8 (+75ns): 254ns self time
                                                Gather#765 step 4/8 (+329ns): scatter:
                                                  Task#751: pool=0
                                                  Task#751 step 1/2 (+0s): 10.084µs self time
                                                  Task#751 step 2/2 (+10.084µs): return nil
                                                  Task#751 ends at 53.985µs
                                                    Gather#751: index=10
                                                    Gather#751 step 1/2 (+0s): 1.025µs self time
                                                    Gather#751 step 2/2 (+1.025µs): return nil
                                                    Gather#751 ends at 55.01µs
                                                Gather#765 step 5/8 (+329ns): 374ns self time
                                                Gather#765 step 6/8 (+703ns): scatter:
                                                  Task#749: pool=0
                                                  Task#749 step 1/2 (+0s): 404ns self time
                                                  Task#749 step 2/2 (+404ns): return nil
                                                  Task#749 ends at 44.679µs
                                                    Gather#749: index=1
                                                    Gather#749 step 1/2 (+0s): 991ns self time
                                                    Gather#749 step 2/2 (+991ns): return nil
                                                    Gather#749 ends at 45.67µs
                                                Gather#765 step 7/8 (+703ns): 133ns self time
                                                Gather#765 step 8/8 (+836ns): return nil
                                                Gather#765 ends at 44.408µs
                                            Gather#772 step 5/6 (+294ns): 463ns self time
                                            Gather#772 step 6/6 (+757ns): return nil
                                            Gather#772 ends at 34.028µs
                                        Combine#777 step 3/4 (+13.311µs): 13.338µs self time
                                        Combine#777 step 4/4 (+26.649µs): return nil
                                        Combine#777 ends at 36.61µs
                                    Plan#28 step 10/11 (+0s): scatter:
                                      Task#747: pool=0
                                      Task#747 step 1/2 (+0s): 0s self time
                                      Task#747 step 2/2 (+0s): return nil
                                      Task#747 ends at 0s
                                        Gather#747: index=8
                                        Gather#747 step 1/2 (+0s): 997ns self time
                                        Gather#747 step 2/2 (+997ns): return nil
                                        Gather#747 ends at 997ns
                                    Plan#28 step 11/11 (+0s): ends at 454.078µs
                                  Task#732 step 3/4 (+454.078µs): 0s self time
                                  Task#732 step 4/4 (+454.078µs): return nil
                                  Task#732 ends at 23.981041ms
                                    Gather#732: index=4
                                    Gather#732 step 1/2 (+0s): 248.866µs self time
                                    Gather#732 step 2/2 (+248.866µs): return nil
                                    Gather#732 ends at 24.229907ms
                                Combine#864 step 11/14 (+924ns): 123ns self time
                                Combine#864 step 12/14 (+1.047µs): scatter:
                                  Task#863: pool=2
                                  Task#863 step 1/2 (+0s): 10.002µs self time
                                  Task#863 step 2/2 (+10.002µs): return nil
                                  Task#863 ends at 23.537088ms
                                    Gather#863: index=4
                                    Gather#863 step 1/4 (+0s): 205.909µs self time
                                    Gather#863 step 2/4 (+205.909µs): scatter:
                                      Task#782: pool=3
                                      Task#782 step 1/2 (+0s): 10.181µs self time
                                      Task#782 step 2/2 (+10.181µs): return nil
                                      Task#782 ends at 23.753178ms
                                        Gather#782: index=5
                                        Gather#782 step 1/2 (+0s): 1.005µs self time
                                        Gather#782 step 2/2 (+1.005µs): return nil
                                        Gather#782 ends at 23.754183ms
                                    Gather#863 step 3/4 (+205.909µs): 206.394µs self time
                                    Gather#863 step 4/4 (+412.303µs): return nil
                                    Gather#863 ends at 23.949391ms
                                Combine#864 step 13/14 (+1.047µs): 20ns self time
                                Combine#864 step 14/14 (+1.067µs): return nil
                                Combine#864 ends at 23.527106ms
                            Combine#894 step 15/16 (+1.004µs): 1ns self time
                            Combine#894 step 16/16 (+1.005µs): return nil
                            Combine#894 ends at 6.780815ms
                        Plan#26 step 2/9 (+0s): scatter:
                          Task#895: pool=1
                          Task#895 step 1/2 (+0s): 4.698µs self time
                          Task#895 step 2/2 (+4.698µs): return error
                          Task#895 ends at 4.698µs
                            Gather#895: index=2
                            Gather#895 step 1/6 (+0s): 331ns self time
                            Gather#895 step 2/6 (+331ns): scatter:
                              Task#709: pool=4
                              Task#709 step 1/2 (+0s): 10.116µs self time
                              Task#709 step 2/2 (+10.116µs): return nil
                              Task#709 ends at 15.145µs
                                Combine#709: index=0 flush=<nil>
                                Combine#709 step 1/2 (+0s): 131.812µs self time
                                Combine#709 step 2/2 (+131.812µs): return nil
                                Combine#709 ends at 146.957µs
                            Gather#895 step 3/6 (+331ns): 624ns self time
                            Gather#895 step 4/6 (+955ns): scatter:
                              Task#835: pool=0
                              Task#835 step 1/2 (+0s): 9.999µs self time
                              Task#835 step 2/2 (+9.999µs): return nil
                              Task#835 ends at 15.652µs
                                Gather#835: index=4
                                Gather#835 step 1/4 (+0s): 498ns self time
                                Gather#835 step 2/4 (+498ns): subjob:
                                  Plan#31: pathCount=16 taskCount=24 maxPathDuration=8.320124ms minGatherCount=19 maxGatherCount=40
                                     TaskPools[0]: TaskPool#99: limit=2
                                     CombinerPools[0]: CombinerPool#143: limit=4
                                     CombinerPools[1]: CombinerPool#144: limit=8
                                     CombinerPools[2]: CombinerPool#145: limit=1
                                     CombinerPools[3]: CombinerPool#146: limit=2
                                     CombinerPools[4]: CombinerPool#147: limit=8
                                     Combiners[0]: pool=2
                                     Combiners[1]: pool=1
                                     Combiners[2]: pool=1
                                     Combiners[3]: pool=1
                                     Combiners[4]: pool=3
                                     Combiners[5]: pool=1
                                     Combiners[6]: pool=0
                                     Combiners[7]: pool=2
                                     Combiners[8]: pool=2
                                     Combiners[9]: pool=0
                                     Combiners[10]: pool=4
                                     Combiners[11]: pool=0
                                     Combiners[12]: pool=3
                                     Combiners[13]: pool=0
                                     Combiners[14]: pool=3
                                     Combiners[15]: pool=2
                                     Combiners[16]: pool=2
                                     Combiners[17]: pool=1
                                     Combiners[18]: pool=0
                                     Combiners[19]: pool=0
                                  Plan#31 step 1/8 (+0s): scatter:
                                    Task#858: pool=0
                                    Task#858 step 1/2 (+0s): 10.056µs self time
                                    Task#858 step 2/2 (+10.056µs): return nil
                                    Task#858 ends at 10.056µs
                                      Gather#858: index=7
                                      Gather#858 step 1/6 (+0s): 12.393µs self time
                                      Gather#858 step 2/6 (+12.393µs): scatter:
                                        Task#855: pool=0
                                        Task#855 step 1/2 (+0s): 10.005µs self time
                                        Task#855 step 2/2 (+10.005µs): return nil
                                        Task#855 ends at 32.454µs
                                          Gather#855: index=11
                                          Gather#855 step 1/12 (+0s): 227ns self time
                                          Gather#855 step 2/12 (+227ns): scatter:
                                            Task#843: pool=0
                                            Task#843 step 1/2 (+0s): 5.918µs self time
                                            Task#843 step 2/2 (+5.918µs): return nil
                                            Task#843 ends at 38.599µs
                                              Gather#843: index=10
                                              Gather#843 step 1/2 (+0s): 998ns self time
                                              Gather#843 step 2/2 (+998ns): return nil
                                              Gather#843 ends at 39.597µs
                                          Gather#855 step 3/12 (+227ns): 161ns self time
                                          Gather#855 step 4/12 (+388ns): scatter:
                                            Task#851: pool=0
                                            Task#851 step 1/2 (+0s): 8.286275ms self time
                                            Task#851 step 2/2 (+8.286275ms): return nil
                                            Task#851 ends at 8.319117ms
                                              Gather#851: index=1
                                              Gather#851 step 1/2 (+0s): 1.007µs self time
                                              Gather#851 step 2/2 (+1.007µs): return nil
                                              Gather#851 ends at 8.320124ms
                                          Gather#855 step 5/12 (+388ns): 66ns self time
                                          Gather#855 step 6/12 (+454ns): scatter:
                                            Task#848: pool=0
                                            Task#848 step 1/2 (+0s): 10.002µs self time
                                            Task#848 step 2/2 (+10.002µs): return nil
                                            Task#848 ends at 42.91µs
                                              Combine#848: index=10 flush=<nil>
                                              Combine#848 step 1/2 (+0s): 632ns self time
                                              Combine#848 step 2/2 (+632ns): return nil
                                              Combine#848 ends at 43.542µs
                                          Gather#855 step 7/12 (+454ns): 292ns self time
                                          Gather#855 step 8/12 (+746ns): scatter:
                                            Task#841: pool=0
                                            Task#841 step 1/2 (+0s): 6.412µs self time
                                            Task#841 step 2/2 (+6.412µs): return nil
                                            Task#841 ends at 39.612µs
                                              Gather#841: index=1
                                              Gather#841 step 1/2 (+0s): 3.161µs self time
                                              Gather#841 step 2/2 (+3.161µs): return nil
                                              Gather#841 ends at 42.773µs
                                          Gather#855 step 9/12 (+746ns): 117ns self time
                                          Gather#855 step 10/12 (+863ns): scatter:
                                            Task#854: pool=0
                                            Task#854 step 1/2 (+0s): 9.994µs self time
                                            Task#854 step 2/2 (+9.994µs): return nil
                                            Task#854 ends at 43.311µs
                                              Gather#854: index=11
                                              Gather#854 step 1/8 (+0s): 283ns self time
                                              Gather#854 step 2/8 (+283ns): scatter:
                                                Task#852: pool=0
                                                Task#852 step 1/2 (+0s): 279ns self time
                                                Task#852 step 2/2 (+279ns): return nil
                                                Task#852 ends at 43.873µs
                                                  Gather#852: index=7
                                                  Gather#852 step 1/8 (+0s): 246ns self time
                                                  Gather#852 step 2/8 (+246ns): scatter:
                                                    Task#838: pool=0
                                                    Task#838 step 1/2 (+0s): 9.44µs self time
                                                    Task#838 step 2/2 (+9.44µs): return nil
                                                    Task#838 ends at 53.559µs
                                                      Gather#838: index=9
                                                      Gather#838 step 1/2 (+0s): 1.003µs self time
                                                      Gather#838 step 2/2 (+1.003µs): return nil
                                                      Gather#838 ends at 54.562µs
                                                  Gather#852 step 3/8 (+246ns): 245ns self time
                                                  Gather#852 step 4/8 (+491ns): scatter:
                                                    Task#842: pool=0
                                                    Task#842 step 1/2 (+0s): 9.998µs self time
                                                    Task#842 step 2/2 (+9.998µs): return nil
                                                    Task#842 ends at 54.362µs
                                                      Gather#842: index=0
                                                      Gather#842 step 1/2 (+0s): 998ns self time
                                                      Gather#842 step 2/2 (+998ns): return nil
                                                      Gather#842 ends at 55.36µs
                                                  Gather#852 step 5/8 (+491ns): 252ns self time
                                                  Gather#852 step 6/8 (+743ns): scatter:
                                                    Task#837: pool=0
                                                    Task#837 step 1/2 (+0s): 283ns self time
                                                    Task#837 step 2/2 (+283ns): return nil
                                                    Task#837 ends at 44.899µs
                                                      Gather#837: index=0
                                                      Gather#837 step 1/2 (+0s): 1µs self time
                                                      Gather#837 step 2/2 (+1µs): return nil
                                                      Gather#837 ends at 45.899µs
                                                  Gather#852 step 7/8 (+743ns): 256ns self time
                                                  Gather#852 step 8/8 (+999ns): return nil
                                                  Gather#852 ends at 44.872µs
                                              Gather#854 step 3/8 (+283ns): 201ns self time
                                              Gather#854 step 4/8 (+484ns): scatter:
                                                Task#844: pool=0
                                                Task#844 step 1/2 (+0s): 10.001µs self time
                                                Task#844 step 2/2 (+10.001µs): return nil
                                                Task#844 ends at 53.796µs
                                                  Gather#844: index=12
                                                  Gather#844 step 1/2 (+0s): 1µs self time
                                                  Gather#844 step 2/2 (+1µs): return nil
                                                  Gather#844 ends at 54.796µs
                                              Gather#854 step 5/8 (+484ns): 264ns self time
                                              Gather#854 step 6/8 (+748ns): scatter:
                                                Task#853: pool=0
                                                Task#853 step 1/2 (+0s): 9.706µs self time
                                                Task#853 step 2/2 (+9.706µs): return error
                                                Task#853 ends at 53.765µs
                                                  Gather#853: index=2
                                                  Gather#853 step 1/4 (+0s): 0s self time
                                                  Gather#853 step 2/4 (+0s): scatter:
                                                    Task#847: pool=0
                                                    Task#847 step 1/2 (+0s): 1.229039ms self time
                                                    Task#847 step 2/2 (+1.229039ms): return nil
                                                    Task#847 ends at 1.282804ms
                                                      Gather#847: index=1
                                                      Gather#847 step 1/2 (+0s): 997ns self time
                                                      Gather#847 step 2/2 (+997ns): return nil
                                                      Gather#847 ends at 1.283801ms
                                                  Gather#853 step 3/4 (+0s): 0s self time
                                                  Gather#853 step 4/4 (+0s): return nil
                                                  Gather#853 ends at 53.765µs
                                              Gather#854 step 7/8 (+748ns): 258ns self time
                                              Gather#854 step 8/8 (+1.006µs): return nil
                                              Gather#854 ends at 44.317µs
                                          Gather#855 step 11/12 (+863ns): 90ns self time
                                          Gather#855 step 12/12 (+953ns): return nil
                                          Gather#855 ends at 33.407µs
                                      Gather#858 step 3/6 (+12.393µs): 9.422µs self time
                                      Gather#858 step 4/6 (+21.815µs): scatter:
                                        Task#850: pool=0
                                        Task#850 step 1/2 (+0s): 9.765µs self time
                                        Task#850 step 2/2 (+9.765µs): return nil
                                        Task#850 ends at 41.636µs
                                          Combine#850: index=0 flush=<nil>
                                          Combine#850 step 1/2 (+0s): 984ns self time
                                          Combine#850 step 2/2 (+984ns): return nil
                                          Combine#850 ends at 42.62µs
                                      Gather#858 step 5/6 (+21.815µs): 9.426µs self time
                                      Gather#858 step 6/6 (+31.241µs): return nil
                                      Gather#858 ends at 41.297µs
                                  Plan#31 step 2/8 (+0s): scatter:
                                    Task#856: pool=0
                                    Task#856 step 1/2 (+0s): 9.995µs self time
                                    Task#856 step 2/2 (+9.995µs): return error
                                    Task#856 ends at 9.995µs
                                      Gather#856: index=0
                                      Gather#856 step 1/4 (+0s): 473ns self time
                                      Gather#856 step 2/4 (+473ns): scatter:
                                        Task#849: pool=0
                                        Task#849 step 1/2 (+0s): 9.999µs self time
                                        Task#849 step 2/2 (+9.999µs): return nil
                                        Task#849 ends at 20.467µs
                                          Gather#849: index=2
                                          Gather#849 step 1/2 (+0s): 999ns self time
                                          Gather#849 step 2/2 (+999ns): return nil
                                          Gather#849 ends at 21.466µs
                                      Gather#856 step 3/4 (+473ns): 534ns self time
                                      Gather#856 step 4/4 (+1.007µs): return nil
                                      Gather#856 ends at 11.002µs
                                  Plan#31 step 3/8 (+0s): scatter:
                                    Task#836: pool=0
                                    Task#836 step 1/2 (+0s): 9.984µs self time
                                    Task#836 step 2/2 (+9.984µs): return nil
                                    Task#836 ends at 9.984µs
                                      Gather#836: index=0
                                      Gather#836 step 1/2 (+0s): 1.001µs self time
                                      Gather#836 step 2/2 (+1.001µs): return nil
                                      Gather#836 ends at 10.985µs
                                  Plan#31 step 4/8 (+0s): scatter:
                                    Task#840: pool=0
                                    Task#840 step 1/2 (+0s): 10.113µs self time
                                    Task#840 step 2/2 (+10.113µs): return nil
                                    Task#840 ends at 10.113µs
                                      Combine#840: index=6 flush=<nil>
                                      Combine#840 step 1/2 (+0s): 999ns self time
                                      Combine#840 step 2/2 (+999ns): return nil
                                      Combine#840 ends at 11.112µs
                                  Plan#31 step 5/8 (+0s): scatter:
                                    Task#846: pool=0
                                    Task#846 step 1/2 (+0s): 10.006µs self time
                                    Task#846 step 2/2 (+10.006µs): return nil
                                    Task#846 ends at 10.006µs
                                      Gather#846: index=11
                                      Gather#846 step 1/2 (+0s): 281ns self time
                                      Gather#846 step 2/2 (+281ns): return nil
                                      Gather#846 ends at 10.287µs
                                  Plan#31 step 6/8 (+0s): scatter:
                                    Task#857: pool=0
                                    Task#857 step 1/2 (+0s): 9.974µs self time
                                    Task#857 step 2/2 (+9.974µs): return nil
                                    Task#857 ends at 9.974µs
                                      Combine#857: index=9 flush=<nil>
                                      Combine#857 step 1/4 (+0s): 12.718µs self time
                                      Combine#857 step 2/4 (+12.718µs): scatter:
                                        Task#839: pool=0
                                        Task#839 step 1/2 (+0s): 9.995µs self time
                                        Task#839 step 2/2 (+9.995µs): return nil
                                        Task#839 ends at 32.687µs
                                          Combine#839: index=13 flush=<nil>
                                          Combine#839 step 1/2 (+0s): 996ns self time
                                          Combine#839 step 2/2 (+996ns): return nil
                                          Combine#839 ends at 33.683µs
                                      Combine#857 step 3/4 (+12.718µs): 7.868µs self time
                                      Combine#857 step 4/4 (+20.586µs): return nil
                                      Combine#857 ends at 30.56µs
                                  Plan#31 step 7/8 (+0s): scatter:
                                    Task#859: pool=0
                                    Task#859 step 1/2 (+0s): 740ns self time
                                    Task#859 step 2/2 (+740ns): return nil
                                    Task#859 ends at 740ns
                                      Gather#859: index=7
                                      Gather#859 step 1/4 (+0s): 364ns self time
                                      Gather#859 step 2/4 (+364ns): scatter:
                                        Task#845: pool=0
                                        Task#845 step 1/2 (+0s): 119.504µs self time
                                        Task#845 step 2/2 (+119.504µs): return nil
                                        Task#845 ends at 120.608µs
                                          Gather#845: index=0
                                          Gather#845 step 1/2 (+0s): 998ns self time
                                          Gather#845 step 2/2 (+998ns): return nil
                                          Gather#845 ends at 121.606µs
                                      Gather#859 step 3/4 (+364ns): 585ns self time
                                      Gather#859 step 4/4 (+949ns): return nil
                                      Gather#859 ends at 1.689µs
                                  Plan#31 step 8/8 (+0s): ends at 8.320124ms
                                Gather#835 step 3/4 (+8.320622ms): 500ns self time
                                Gather#835 step 4/4 (+8.321122ms): return error
                                Gather#835 ends at 8.336774ms
                            Gather#895 step 5/6 (+955ns): 37ns self time
                            Gather#895 step 6/6 (+992ns): return nil
                            Gather#895 ends at 5.69µs
                        Plan#26 step 3/9 (+0s): scatter:
                          Task#705: pool=3
                          Task#705 step 1/2 (+0s): 10.006µs self time
                          Task#705 step 2/2 (+10.006µs): return nil
                          Task#705 ends at 10.006µs
                            Gather#705: index=5
                            Gather#705 step 1/2 (+0s): 56.137µs self time
                            Gather#705 step 2/2 (+56.137µs): return nil
                            Gather#705 ends at 66.143µs
                        Plan#26 step 4/9 (+0s): scatter:
                          Task#708: pool=1
                          Task#708 step 1/2 (+0s): 7.6µs self time
                          Task#708 step 2/2 (+7.6µs): return nil
                          Task#708 ends at 7.6µs
                            Gather#708: index=1
                            Gather#708 step 1/2 (+0s): 2.713µs self time
                            Gather#708 step 2/2 (+2.713µs): return error
                            Gather#708 ends at 10.313µs
                        Plan#26 step 5/9 (+0s): scatter:
                          Task#731: pool=0
                          Task#731 step 1/2 (+0s): 14.656µs self time
                          Task#731 step 2/2 (+14.656µs): return nil
                          Task#731 ends at 14.656µs
                            Gather#731: index=5
                            Gather#731 step 1/2 (+0s): 756ns self time
                            Gather#731 step 2/2 (+756ns): return nil
                            Gather#731 ends at 15.412µs
                        Plan#26 step 6/9 (+0s): scatter:
                          Task#813: pool=0
                          Task#813 step 1/2 (+0s): 9.998µs self time
                          Task#813 step 2/2 (+9.998µs): return nil
                          Task#813 ends at 9.998µs
                            Combine#813: index=0 flush=<nil>
                            Combine#813 step 1/4 (+0s): 474.076µs self time
                            Combine#813 step 2/4 (+474.076µs): subjob:
                              Plan#30: pathCount=15 taskCount=19 maxPathDuration=9.751329ms minGatherCount=13 maxGatherCount=37
                                 TaskPools[0]: TaskPool#97: limit=9
                                 TaskPools[1]: TaskPool#98: limit=2
                                 CombinerPools[0]: CombinerPool#141: limit=4
                                 CombinerPools[1]: CombinerPool#142: limit=4
                                 Combiners[0]: pool=0
                                 Combiners[1]: pool=0
                                 Combiners[2]: pool=0
                                 Combiners[3]: pool=0
                                 Combiners[4]: pool=1
                                 Combiners[5]: pool=0
                                 Combiners[6]: pool=1
                                 Combiners[7]: pool=1
                                 Combiners[8]: pool=0
                              Plan#30 step 1/4 (+0s): scatter:
                                Task#821: pool=1
                                Task#821 step 1/2 (+0s): 11.517µs self time
                                Task#821 step 2/2 (+11.517µs): return nil
                                Task#821 ends at 11.517µs
                                  Gather#821: index=0
                                  Gather#821 step 1/2 (+0s): 1.707µs self time
                                  Gather#821 step 2/2 (+1.707µs): return nil
                                  Gather#821 ends at 13.224µs
                              Plan#30 step 2/4 (+0s): scatter:
                                Task#832: pool=0
                                Task#832 step 1/2 (+0s): 8.824106ms self time
                                Task#832 step 2/2 (+8.824106ms): return nil
                                Task#832 ends at 8.824106ms
                                  Gather#832: index=3
                                  Gather#832 step 1/6 (+0s): 480ns self time
                                  Gather#832 step 2/6 (+480ns): scatter:
                                    Task#831: pool=0
                                    Task#831 step 1/2 (+0s): 9.998µs self time
                                    Task#831 step 2/2 (+9.998µs): return nil
                                    Task#831 ends at 8.834584ms
                                      Gather#831: index=1
                                      Gather#831 step 1/12 (+0s): 138.369µs self time
                                      Gather#831 step 2/12 (+138.369µs): scatter:
                                        Task#822: pool=0
                                        Task#822 step 1/2 (+0s): 10.001µs self time
                                        Task#822 step 2/2 (+10.001µs): return nil
                                        Task#822 ends at 8.982954ms
                                          Combine#822: index=1 flush=<nil>
                                          Combine#822 step 1/2 (+0s): 993ns self time
                                          Combine#822 step 2/2 (+993ns): return nil
                                          Combine#822 ends at 8.983947ms
                                      Gather#831 step 3/12 (+138.369µs): 138.339µs self time
                                      Gather#831 step 4/12 (+276.708µs): scatter:
                                        Task#816: pool=0
                                        Task#816 step 1/2 (+0s): 9.975µs self time
                                        Task#816 step 2/2 (+9.975µs): return nil
                                        Task#816 ends at 9.121267ms
                                          Gather#816: index=4
                                          Gather#816 step 1/2 (+0s): 3.544µs self time
                                          Gather#816 step 2/2 (+3.544µs): return nil
                                          Gather#816 ends at 9.124811ms
                                      Gather#831 step 5/12 (+276.708µs): 138.371µs self time
                                      Gather#831 step 6/12 (+415.079µs): scatter:
                                        Task#817: pool=1
                                        Task#817 step 1/2 (+0s): 41.162µs self time
                                        Task#817 step 2/2 (+41.162µs): return nil
                                        Task#817 ends at 9.290825ms
                                          Combine#817: index=1 flush=<nil>
                                          Combine#817 step 1/2 (+0s): 995ns self time
                                          Combine#817 step 2/2 (+995ns): return nil
                                          Combine#817 ends at 9.29182ms
                                      Gather#831 step 7/12 (+415.079µs): 138.373µs self time
                                      Gather#831 step 8/12 (+553.452µs): scatter:
                                        Task#830: pool=0
                                        Task#830 step 1/2 (+0s): 9.99µs self time
                                        Task#830 step 2/2 (+9.99µs): return nil
                                        Task#830 ends at 9.398026ms
                                          Gather#830: index=3
                                          Gather#830 step 1/16 (+0s): 89ns self time
                                          Gather#830 step 2/16 (+89ns): scatter:
                                            Task#829: pool=1
                                            Task#829 step 1/2 (+0s): 13.367µs self time
                                            Task#829 step 2/2 (+13.367µs): return nil
                                            Task#829 ends at 9.411482ms
                                              Gather#829: index=3
                                              Gather#829 step 1/6 (+0s): 327ns self time
                                              Gather#829 step 2/6 (+327ns): scatter:
                                                Task#823: pool=0
                                                Task#823 step 1/2 (+0s): 313.562µs self time
                                                Task#823 step 2/2 (+313.562µs): return error
                                                Task#823 ends at 9.725371ms
                                                  Combine#823: index=7 flush=<nil>
                                                  Combine#823 step 1/2 (+0s): 999ns self time
                                                  Combine#823 step 2/2 (+999ns): return nil
                                                  Combine#823 ends at 9.72637ms
                                              Gather#829 step 3/6 (+327ns): 94ns self time
                                              Gather#829 step 4/6 (+421ns): scatter:
                                                Task#819: pool=1
                                                Task#819 step 1/2 (+0s): 9.996µs self time
                                                Task#819 step 2/2 (+9.996µs): return nil
                                                Task#819 ends at 9.421899ms
                                                  Combine#819: index=4 flush=<nil>
                                                  Combine#819 step 1/2 (+0s): 818ns self time
                                                  Combine#819 step 2/2 (+818ns): return nil
                                                  Combine#819 ends at 9.422717ms
                                              Gather#829 step 5/6 (+421ns): 562ns self time
                                              Gather#829 step 6/6 (+983ns): return error
                                              Gather#829 ends at 9.412465ms
                                          Gather#830 step 3/16 (+89ns): 78ns self time
                                          Gather#830 step 4/16 (+167ns): scatter:
                                            Task#824: pool=1
                                            Task#824 step 1/2 (+0s): 9.979µs self time
                                            Task#824 step 2/2 (+9.979µs): return error
                                            Task#824 ends at 9.408172ms
                                              Combine#824: index=8 flush=Gather#824
                                              Combine#824 step 1/2 (+0s): 1.098µs self time
                                              Combine#824 step 2/2 (+1.098µs): return nil
                                              Combine#824 ends at 9.40927ms
                                                Gather#824: index=1
                                                Gather#824 step 1/2 (+0s): 0s self time
                                                Gather#824 step 2/2 (+0s): return nil
                                                Gather#824 ends at 0s
                                          Gather#830 step 5/16 (+167ns): 28ns self time
                                          Gather#830 step 6/16 (+195ns): scatter:
                                            Task#827: pool=1
                                            Task#827 step 1/2 (+0s): 7.065µs self time
                                            Task#827 step 2/2 (+7.065µs): return nil
                                            Task#827 ends at 9.405286ms
                                              Combine#827: index=2 flush=<nil>
                                              Combine#827 step 1/2 (+0s): 1.26µs self time
                                              Combine#827 step 2/2 (+1.26µs): return nil
                                              Combine#827 ends at 9.406546ms
                                          Gather#830 step 7/16 (+195ns): 199ns self time
                                          Gather#830 step 8/16 (+394ns): scatter:
                                            Task#826: pool=1
                                            Task#826 step 1/2 (+0s): 10.001µs self time
                                            Task#826 step 2/2 (+10.001µs): return nil
                                            Task#826 ends at 9.408421ms
                                              Gather#826: index=1
                                              Gather#826 step 1/2 (+0s): 2.747µs self time
                                              Gather#826 step 2/2 (+2.747µs): return nil
                                              Gather#826 ends at 9.411168ms
                                          Gather#830 step 9/16 (+394ns): 64ns self time
                                          Gather#830 step 10/16 (+458ns): scatter:
                                            Task#815: pool=0
                                            Task#815 step 1/2 (+0s): 10.003µs self time
                                            Task#815 step 2/2 (+10.003µs): return nil
                                            Task#815 ends at 9.408487ms
                                              Gather#815: index=3
                                              Gather#815 step 1/2 (+0s): 1.813µs self time
                                              Gather#815 step 2/2 (+1.813µs): return nil
                                              Gather#815 ends at 9.4103ms
                                          Gather#830 step 11/16 (+458ns): 197ns self time
                                          Gather#830 step 12/16 (+655ns): scatter:
                                            Task#814: pool=1
                                            Task#814 step 1/2 (+0s): 9.998µs self time
                                            Task#814 step 2/2 (+9.998µs): return nil
                                            Task#814 ends at 9.408679ms
                                              Gather#814: index=0
                                              Gather#814 step 1/2 (+0s): 349ns self time
                                              Gather#814 step 2/2 (+349ns): return nil
                                              Gather#814 ends at 9.409028ms
                                          Gather#830 step 13/16 (+655ns): 0s self time
                                          Gather#830 step 14/16 (+655ns): scatter:
                                            Task#825: pool=0
                                            Task#825 step 1/2 (+0s): 10.422µs self time
                                            Task#825 step 2/2 (+10.422µs): return nil
                                            Task#825 ends at 9.409103ms
                                              Gather#825: index=1
                                              Gather#825 step 1/2 (+0s): 1.006µs self time
                                              Gather#825 step 2/2 (+1.006µs): return nil
                                              Gather#825 ends at 9.410109ms
                                          Gather#830 step 15/16 (+655ns): 0s self time
                                          Gather#830 step 16/16 (+655ns): return nil
                                          Gather#830 ends at 9.398681ms
                                      Gather#831 step 9/12 (+553.452µs): 138.434µs self time
                                      Gather#831 step 10/12 (+691.886µs): scatter:
                                        Task#818: pool=0
                                        Task#818 step 1/2 (+0s): 224.446µs self time
                                        Task#818 step 2/2 (+224.446µs): return nil
                                        Task#818 ends at 9.750916ms
                                          Gather#818: index=4
                                          Gather#818 step 1/2 (+0s): 413ns self time
                                          Gather#818 step 2/2 (+413ns): return nil
                                          Gather#818 ends at 9.751329ms
                                      Gather#831 step 11/12 (+691.886µs): 138.315µs self time
                                      Gather#831 step 12/12 (+830.201µs): return nil
                                      Gather#831 ends at 9.664785ms
                                  Gather#832 step 3/6 (+480ns): 263ns self time
                                  Gather#832 step 4/6 (+743ns): scatter:
                                    Task#828: pool=0
                                    Task#828 step 1/2 (+0s): 58.671µs self time
                                    Task#828 step 2/2 (+58.671µs): return nil
                                    Task#828 ends at 8.88352ms
                                      Gather#828: index=4
                                      Gather#828 step 1/2 (+0s): 973ns self time
                                      Gather#828 step 2/2 (+973ns): return nil
                                      Gather#828 ends at 8.884493ms
                                  Gather#832 step 5/6 (+743ns): 743ns self time
                                  Gather#832 step 6/6 (+1.486µs): return nil
                                  Gather#832 ends at 8.825592ms
                              Plan#30 step 3/4 (+0s): scatter:
                                Task#820: pool=1
                                Task#820 step 1/2 (+0s): 10.23µs self time
                                Task#820 step 2/2 (+10.23µs): return nil
                                Task#820 ends at 10.23µs
                                  Combine#820: index=3 flush=<nil>
                                  Combine#820 step 1/2 (+0s): 949ns self time
                                  Combine#820 step 2/2 (+949ns): return nil
                                  Combine#820 ends at 11.179µs
                              Plan#30 step 4/4 (+0s): ends at 9.751329ms
                            Combine#813 step 3/4 (+10.225405ms): 474.052µs self time
                            Combine#813 step 4/4 (+10.699457ms): return nil
                            Combine#813 ends at 10.709455ms
                        Plan#26 step 7/9 (+0s): scatter:
                          Task#783: pool=3
                          Task#783 step 1/4 (+0s): 1.102µs self time
                          Task#783 step 2/4 (+1.102µs): subjob:
                            Plan#29: pathCount=18 taskCount=29 maxPathDuration=11.041183ms minGatherCount=19 maxGatherCount=52
                               TaskPools[0]: TaskPool#87: limit=10
                               TaskPools[1]: TaskPool#88: limit=6
                               TaskPools[2]: TaskPool#89: limit=10
                               TaskPools[3]: TaskPool#90: limit=1
                               TaskPools[4]: TaskPool#91: limit=3
                               TaskPools[5]: TaskPool#92: limit=2
                               TaskPools[6]: TaskPool#93: limit=4
                               TaskPools[7]: TaskPool#94: limit=8
                               TaskPools[8]: TaskPool#95: limit=2
                               TaskPools[9]: TaskPool#96: limit=7
                               CombinerPools[0]: CombinerPool#137: limit=1
                               CombinerPools[1]: CombinerPool#138: limit=6
                               CombinerPools[2]: CombinerPool#139: limit=6
                               CombinerPools[3]: CombinerPool#140: limit=4
                               Combiners[0]: pool=1
                               Combiners[1]: pool=0
                               Combiners[2]: pool=3
                               Combiners[3]: pool=3
                               Combiners[4]: pool=3
                               Combiners[5]: pool=0
                               Combiners[6]: pool=3
                               Combiners[7]: pool=0
                               Combiners[8]: pool=3
                               Combiners[9]: pool=2
                               Combiners[10]: pool=3
                               Combiners[11]: pool=3
                               Combiners[12]: pool=3
                               Combiners[13]: pool=0
                            Plan#29 step 1/10 (+0s): scatter:
                              Task#808: pool=2
                              Task#808 step 1/2 (+0s): 9.642µs self time
                              Task#808 step 2/2 (+9.642µs): return nil
                              Task#808 ends at 9.642µs
                                Gather#808: index=0
                                Gather#808 step 1/4 (+0s): 998ns self time
                                Gather#808 step 2/4 (+998ns): scatter:
                                  Task#800: pool=1
                                  Task#800 step 1/2 (+0s): 10.001µs self time
                                  Task#800 step 2/2 (+10.001µs): return nil
                                  Task#800 ends at 20.641µs
                                    Gather#800: index=0
                                    Gather#800 step 1/2 (+0s): 1.001µs self time
                                    Gather#800 step 2/2 (+1.001µs): return nil
                                    Gather#800 ends at 21.642µs
                                Gather#808 step 3/4 (+998ns): 0s self time
                                Gather#808 step 4/4 (+998ns): return nil
                                Gather#808 ends at 10.64µs
                            Plan#29 step 2/10 (+0s): scatter:
                              Task#811: pool=3
                              Task#811 step 1/2 (+0s): 9.918µs self time
                              Task#811 step 2/2 (+9.918µs): return nil
                              Task#811 ends at 9.918µs
                                Combine#811: index=1 flush=<nil>
                                Combine#811 step 1/8 (+0s): 108ns self time
                                Combine#811 step 2/8 (+108ns): scatter:
                                  Task#799: pool=0
                                  Task#799 step 1/2 (+0s): 10.001µs self time
                                  Task#799 step 2/2 (+10.001µs): return nil
                                  Task#799 ends at 20.027µs
                                    Combine#799: index=6 flush=<nil>
                                    Combine#799 step 1/2 (+0s): 1.038µs self time
                                    Combine#799 step 2/2 (+1.038µs): return nil
                                    Combine#799 ends at 21.065µs
                                Combine#811 step 3/8 (+108ns): 321ns self time
                                Combine#811 step 4/8 (+429ns): scatter:
                                  Task#801: pool=2
                                  Task#801 step 1/2 (+0s): 10µs self time
                                  Task#801 step 2/2 (+10µs): return error
                                  Task#801 ends at 20.347µs
                                    Gather#801: index=0
                                    Gather#801 step 1/2 (+0s): 991ns self time
                                    Gather#801 step 2/2 (+991ns): return nil
                                    Gather#801 ends at 21.338µs
                                Combine#811 step 5/8 (+429ns): 316ns self time
                                Combine#811 step 6/8 (+745ns): scatter:
                                  Task#794: pool=0
                                  Task#794 step 1/2 (+0s): 9.191µs self time
                                  Task#794 step 2/2 (+9.191µs): return nil
                                  Task#794 ends at 19.854µs
                                    Gather#794: index=0
                                    Gather#794 step 1/2 (+0s): 438.154µs self time
                                    Gather#794 step 2/2 (+438.154µs): return nil
                                    Gather#794 ends at 458.008µs
                                Combine#811 step 7/8 (+745ns): 321ns self time
                                Combine#811 step 8/8 (+1.066µs): return nil
                                Combine#811 ends at 10.984µs
                            Plan#29 step 3/10 (+0s): scatter:
                              Task#810: pool=8
                              Task#810 step 1/2 (+0s): 28.675µs self time
                              Task#810 step 2/2 (+28.675µs): return nil
                              Task#810 ends at 28.675µs
                                Combine#810: index=13 flush=<nil>
                                Combine#810 step 1/6 (+0s): 1.098µs self time
                                Combine#810 step 2/6 (+1.098µs): scatter:
                                  Task#784: pool=7
                                  Task#784 step 1/2 (+0s): 10.019µs self time
                                  Task#784 step 2/2 (+10.019µs): return nil
                                  Task#784 ends at 39.792µs
                                    Combine#784: index=13 flush=<nil>
                                    Combine#784 step 1/2 (+0s): 996ns self time
                                    Combine#784 step 2/2 (+996ns): return nil
                                    Combine#784 ends at 40.788µs
                                Combine#810 step 3/6 (+1.098µs): 777ns self time
                                Combine#810 step 4/6 (+1.875µs): scatter:
                                  Task#807: pool=1
                                  Task#807 step 1/2 (+0s): 10µs self time
                                  Task#807 step 2/2 (+10µs): return nil
                                  Task#807 ends at 40.55µs
                                    Gather#807: index=0
                                    Gather#807 step 1/8 (+0s): 260ns self time
                                    Gather#807 step 2/8 (+260ns): scatter:
                                      Task#806: pool=1
                                      Task#806 step 1/2 (+0s): 10.001µs self time
                                      Task#806 step 2/2 (+10.001µs): return nil
                                      Task#806 ends at 50.811µs
                                        Gather#806: index=0
                                        Gather#806 step 1/4 (+0s): 260ns self time
                                        Gather#806 step 2/4 (+260ns): scatter:
                                          Task#804: pool=0
                                          Task#804 step 1/2 (+0s): 42.293µs self time
                                          Task#804 step 2/2 (+42.293µs): return nil
                                          Task#804 ends at 93.364µs
                                            Gather#804: index=0
                                            Gather#804 step 1/4 (+0s): 500.51µs self time
                                            Gather#804 step 2/4 (+500.51µs): scatter:
                                              Task#795: pool=1
                                              Task#795 step 1/2 (+0s): 9.844µs self time
                                              Task#795 step 2/2 (+9.844µs): return nil
                                              Task#795 ends at 603.718µs
                                                Gather#795: index=0
                                                Gather#795 step 1/2 (+0s): 1.017µs self time
                                                Gather#795 step 2/2 (+1.017µs): return nil
                                                Gather#795 ends at 604.735µs
                                            Gather#804 step 3/4 (+500.51µs): 499.49µs self time
                                            Gather#804 step 4/4 (+1ms): return nil
                                            Gather#804 ends at 1.093364ms
                                        Gather#806 step 3/4 (+260ns): 263ns self time
                                        Gather#806 step 4/4 (+523ns): return nil
                                        Gather#806 ends at 51.334µs
                                    Gather#807 step 3/8 (+260ns): 373ns self time
                                    Gather#807 step 4/8 (+633ns): scatter:
                                      Task#796: pool=4
                                      Task#796 step 1/2 (+0s): 10ms self time
                                      Task#796 step 2/2 (+10ms): return nil
                                      Task#796 ends at 10.041183ms
                                        Gather#796: index=0
                                        Gather#796 step 1/2 (+0s): 1ms self time
                                        Gather#796 step 2/2 (+1ms): return nil
                                        Gather#796 ends at 11.041183ms
                                    Gather#807 step 5/8 (+633ns): 200ns self time
                                    Gather#807 step 6/8 (+833ns): scatter:
                                      Task#805: pool=2
                                      Task#805 step 1/2 (+0s): 9.806µs self time
                                      Task#805 step 2/2 (+9.806µs): return nil
                                      Task#805 ends at 51.189µs
                                        Gather#805: index=0
                                        Gather#805 step 1/10 (+0s): 179ns self time
                                        Gather#805 step 2/10 (+179ns): scatter:
                                          Task#803: pool=1
                                          Task#803 step 1/2 (+0s): 15.061µs self time
                                          Task#803 step 2/2 (+15.061µs): return nil
                                          Task#803 ends at 66.429µs
                                            Combine#803: index=3 flush=<nil>
                                            Combine#803 step 1/4 (+0s): 563ns self time
                                            Combine#803 step 2/4 (+563ns): scatter:
                                              Task#797: pool=8
                                              Task#797 step 1/2 (+0s): 9.997µs self time
                                              Task#797 step 2/2 (+9.997µs): return nil
                                              Task#797 ends at 76.989µs
                                                Gather#797: index=0
                                                Gather#797 step 1/2 (+0s): 992ns self time
                                                Gather#797 step 2/2 (+992ns): return nil
                                                Gather#797 ends at 77.981µs
                                            Combine#803 step 3/4 (+563ns): 566ns self time
                                            Combine#803 step 4/4 (+1.129µs): return nil
                                            Combine#803 ends at 67.558µs
                                        Gather#805 step 3/10 (+179ns): 383ns self time
                                        Gather#805 step 4/10 (+562ns): scatter:
                                          Task#790: pool=0
                                          Task#790 step 1/2 (+0s): 10.163µs self time
                                          Task#790 step 2/2 (+10.163µs): return nil
                                          Task#790 ends at 61.914µs
                                            Combine#790: index=4 flush=<nil>
                                            Combine#790 step 1/2 (+0s): 1.318µs self time
                                            Combine#790 step 2/2 (+1.318µs): return nil
                                            Combine#790 ends at 63.232µs
                                        Gather#805 step 5/10 (+562ns): 173ns self time
                                        Gather#805 step 6/10 (+735ns): scatter:
                                          Task#802: pool=8
                                          Task#802 step 1/2 (+0s): 9.999µs self time
                                          Task#802 step 2/2 (+9.999µs): return nil
                                          Task#802 ends at 61.923µs
                                            Gather#802: index=0
                                            Gather#802 step 1/6 (+0s): 370ns self time
                                            Gather#802 step 2/6 (+370ns): scatter:
                                              Task#792: pool=6
                                              Task#792 step 1/2 (+0s): 6.803013ms self time
                                              Task#792 step 2/2 (+6.803013ms): return nil
                                              Task#792 ends at 6.865306ms
                                                Combine#792: index=12 flush=<nil>
                                                Combine#792 step 1/2 (+0s): 1.007µs self time
                                                Combine#792 step 2/2 (+1.007µs): return nil
                                                Combine#792 ends at 6.866313ms
                                            Gather#802 step 3/6 (+370ns): 350ns self time
                                            Gather#802 step 4/6 (+720ns): scatter:
                                              Task#787: pool=9
                                              Task#787 step 1/2 (+0s): 13.106µs self time
                                              Task#787 step 2/2 (+13.106µs): return nil
                                              Task#787 ends at 75.749µs
                                                Gather#787: index=0
                                                Gather#787 step 1/2 (+0s): 871ns self time
                                                Gather#787 step 2/2 (+871ns): return nil
                                                Gather#787 ends at 76.62µs
                                            Gather#802 step 5/6 (+720ns): 390ns self time
                                            Gather#802 step 6/6 (+1.11µs): return nil
                                            Gather#802 ends at 63.033µs
                                        Gather#805 step 7/10 (+735ns): 171ns self time
                                        Gather#805 step 8/10 (+906ns): scatter:
                                          Task#791: pool=0
                                          Task#791 step 1/2 (+0s): 9.999µs self time
                                          Task#791 step 2/2 (+9.999µs): return nil
                                          Task#791 ends at 62.094µs
                                            Gather#791: index=0
                                            Gather#791 step 1/2 (+0s): 437ns self time
                                            Gather#791 step 2/2 (+437ns): return nil
                                            Gather#791 ends at 62.531µs
                                        Gather#805 step 9/10 (+906ns): 177ns self time
                                        Gather#805 step 10/10 (+1.083µs): return nil
                                        Gather#805 ends at 52.272µs
                                    Gather#807 step 7/8 (+833ns): 197ns self time
                                    Gather#807 step 8/8 (+1.03µs): return nil
                                    Gather#807 ends at 41.58µs
                                Combine#810 step 5/6 (+1.875µs): 783ns self time
                                Combine#810 step 6/6 (+2.658µs): return nil
                                Combine#810 ends at 31.333µs
                            Plan#29 step 4/10 (+0s): scatter:
                              Task#798: pool=9
                              Task#798 step 1/2 (+0s): 9.999µs self time
                              Task#798 step 2/2 (+9.999µs): return nil
                              Task#798 ends at 9.999µs
                                Combine#798: index=10 flush=<nil>
                                Combine#798 step 1/2 (+0s): 787ns self time
                                Combine#798 step 2/2 (+787ns): return nil
                                Combine#798 ends at 10.786µs
                            Plan#29 step 5/10 (+0s): scatter:
                              Task#788: pool=1
                              Task#788 step 1/2 (+0s): 9.995µs self time
                              Task#788 step 2/2 (+9.995µs): return nil
                              Task#788 ends at 9.995µs
                                Gather#788: index=0
                                Gather#788 step 1/2 (+0s): 995ns self time
                                Gather#788 step 2/2 (+995ns): return nil
                                Gather#788 ends at 10.99µs
                            Plan#29 step 6/10 (+0s): scatter:
                              Task#812: pool=0
                              Task#812 step 1/2 (+0s): 4.782148ms self time
                              Task#812 step 2/2 (+4.782148ms): return nil
                              Task#812 ends at 4.782148ms
                                Combine#812: index=0 flush=<nil>
                                Combine#812 step 1/4 (+0s): 9.414µs self time
                                Combine#812 step 2/4 (+9.414µs): scatter:
                                  Task#789: pool=5
                                  Task#789 step 1/2 (+0s): 9.943µs self time
                                  Task#789 step 2/2 (+9.943µs): return nil
                                  Task#789 ends at 4.801505ms
                                    Gather#789: index=0
                                    Gather#789 step 1/2 (+0s): 144ns self time
                                    Gather#789 step 2/2 (+144ns): return nil
                                    Gather#789 ends at 4.801649ms
                                Combine#812 step 3/4 (+9.414µs): 12.191µs self time
                                Combine#812 step 4/4 (+21.605µs): return nil
                                Combine#812 ends at 4.803753ms
                            Plan#29 step 7/10 (+0s): scatter:
                              Task#793: pool=5
                              Task#793 step 1/2 (+0s): 10.007µs self time
                              Task#793 step 2/2 (+10.007µs): return nil
                              Task#793 ends at 10.007µs
                                Gather#793: index=0
                                Gather#793 step 1/2 (+0s): 354.897µs self time
                                Gather#793 step 2/2 (+354.897µs): return nil
                                Gather#793 ends at 364.904µs
                            Plan#29 step 8/10 (+0s): scatter:
                              Task#785: pool=0
                              Task#785 step 1/2 (+0s): 10µs self time
                              Task#785 step 2/2 (+10µs): return nil
                              Task#785 ends at 10µs
                                Combine#785: index=10 flush=<nil>
                                Combine#785 step 1/2 (+0s): 1.041µs self time
                                Combine#785 step 2/2 (+1.041µs): return nil
                                Combine#785 ends at 11.041µs
                            Plan#29 step 9/10 (+0s): scatter:
                              Task#809: pool=0
                              Task#809 step 1/2 (+0s): 9.933µs self time
                              Task#809 step 2/2 (+9.933µs): return nil
                              Task#809 ends at 9.933µs
                                Gather#809: index=0
                                Gather#809 step 1/4 (+0s): 599ns self time
                                Gather#809 step 2/4 (+599ns): scatter:
                                  Task#786: pool=4
                                  Task#786 step 1/2 (+0s): 9.996µs self time
                                  Task#786 step 2/2 (+9.996µs): return nil
                                  Task#786 ends at 20.528µs
                                    Gather#786: index=0
                                    Gather#786 step 1/2 (+0s): 715ns self time
                                    Gather#786 step 2/2 (+715ns): return error
                                    Gather#786 ends at 21.243µs
                                Gather#809 step 3/4 (+599ns): 431ns self time
                                Gather#809 step 4/4 (+1.03µs): return nil
                                Gather#809 ends at 10.963µs
                            Plan#29 step 10/10 (+0s): ends at 11.041183ms
                          Task#783 step 3/4 (+11.042285ms): 1.113µs self time
                          Task#783 step 4/4 (+11.043398ms): return nil
                          Task#783 ends at 11.043398ms
                            Combine#783: index=0 flush=<nil>
                            Combine#783 step 1/2 (+0s): 1.009µs self time
                            Combine#783 step 2/2 (+1.009µs): return nil
                            Combine#783 ends at 11.044407ms
                        Plan#26 step 8/9 (+0s): scatter:
                          Task#712: pool=4
                          Task#712 step 1/2 (+0s): 9.998µs self time
                          Task#712 step 2/2 (+9.998µs): return nil
                          Task#712 ends at 9.998µs
                            Gather#712: index=3
                            Gather#712 step 1/2 (+0s): 1.001µs self time
                            Gather#712 step 2/2 (+1.001µs): return nil
                            Gather#712 ends at 10.999µs
                        Plan#26 step 9/9 (+0s): ends at 28.307391ms
                      Gather#704 step 5/6 (+28.308137ms): 255ns self time
                      Gather#704 step 6/6 (+28.308392ms): return nil
                      Gather#704 ends at 33.344329ms
                  Gather#899 step 3/4 (+507ns): 494ns self time
                  Gather#899 step 4/4 (+1.001µs): return nil
                  Gather#899 ends at 5.026438ms
              Plan#12 step 7/10 (+0s): scatter:
                Task#270: pool=0
                Task#270 step 1/2 (+0s): 0s self time
                Task#270 step 2/2 (+0s): return nil
                Task#270 ends at 0s
                  Gather#270: index=0
                  Gather#270 step 1/2 (+0s): 998ns self time
                  Gather#270 step 2/2 (+998ns): return nil
                  Gather#270 ends at 998ns
              Plan#12 step 8/10 (+0s): scatter:
                Task#898: pool=0
                Task#898 step 1/2 (+0s): 9.999µs self time
                Task#898 step 2/2 (+9.999µs): return nil
                Task#898 ends at 9.999µs
                  Combine#898: index=1 flush=<nil>
                  Combine#898 step 1/4 (+0s): 499ns self time
                  Combine#898 step 2/4 (+499ns): scatter:
                    Task#269: pool=0
                    Task#269 step 1/2 (+0s): 9.893µs self time
                    Task#269 step 2/2 (+9.893µs): return nil
                    Task#269 ends at 20.391µs
                      Gather#269: index=3
                      Gather#269 step 1/2 (+0s): 980ns self time
                      Gather#269 step 2/2 (+980ns): return nil
                      Gather#269 ends at 21.371µs
                  Combine#898 step 3/4 (+499ns): 502ns self time
                  Combine#898 step 4/4 (+1.001µs): return nil
                  Combine#898 ends at 11µs
              Plan#12 step 9/10 (+0s): scatter:
                Task#900: pool=0
                Task#900 step 1/4 (+0s): 0s self time
                Task#900 step 2/4 (+0s): subjob:
                  Plan#36: pathCount=5 taskCount=12 maxPathDuration=15.826313ms minGatherCount=7 maxGatherCount=22
                     TaskPools[0]: TaskPool#117: limit=10
                     TaskPools[1]: TaskPool#118: limit=3
                     TaskPools[2]: TaskPool#119: limit=1
                     TaskPools[3]: TaskPool#120: limit=9
                     TaskPools[4]: TaskPool#121: limit=7
                     TaskPools[5]: TaskPool#122: limit=1
                     CombinerPools[0]: CombinerPool#158: limit=2
                     CombinerPools[1]: CombinerPool#159: limit=3
                     CombinerPools[2]: CombinerPool#160: limit=3
                     Combiners[0]: pool=2
                     Combiners[1]: pool=1
                     Combiners[2]: pool=1
                     Combiners[3]: pool=2
                  Plan#36 step 1/5 (+0s): scatter:
                    Task#1053: pool=2
                    Task#1053 step 1/2 (+0s): 7.968523ms self time
                    Task#1053 step 2/2 (+7.968523ms): return nil
                    Task#1053 ends at 7.968523ms
                      Combine#1053: index=0 flush=<nil>
                      Combine#1053 step 1/4 (+0s): 604.968µs self time
                      Combine#1053 step 2/4 (+604.968µs): scatter:
                        Task#997: pool=2
                        Task#997 step 1/2 (+0s): 10µs self time
                        Task#997 step 2/2 (+10µs): return nil
                        Task#997 ends at 8.583491ms
                          Gather#997: index=0
                          Gather#997 step 1/4 (+0s): 487ns self time
                          Gather#997 step 2/4 (+487ns): subjob:
                            Plan#37: pathCount=15 taskCount=19 maxPathDuration=44.401µs minGatherCount=14 maxGatherCount=22
                               TaskPools[0]: TaskPool#123: limit=5
                               TaskPools[1]: TaskPool#124: limit=3
                               TaskPools[2]: TaskPool#125: limit=1
                               TaskPools[3]: TaskPool#126: limit=3
                               TaskPools[4]: TaskPool#127: limit=1
                               CombinerPools[0]: CombinerPool#161: limit=1
                               CombinerPools[1]: CombinerPool#162: limit=1
                               CombinerPools[2]: CombinerPool#163: limit=4
                               Combiners[0]: pool=2
                               Combiners[1]: pool=1
                               Combiners[2]: pool=2
                               Combiners[3]: pool=1
                               Combiners[4]: pool=1
                               Combiners[5]: pool=0
                               Combiners[6]: pool=1
                               Combiners[7]: pool=0
                            Plan#37 step 1/9 (+0s): scatter:
                              Task#1016: pool=2
                              Task#1016 step 1/2 (+0s): 8.922µs self time
                              Task#1016 step 2/2 (+8.922µs): return nil
                              Task#1016 ends at 8.922µs
                                Gather#1016: index=0
                                Gather#1016 step 1/10 (+0s): 60ns self time
                                Gather#1016 step 2/10 (+60ns): scatter:
                                  Task#1015: pool=3
                                  Task#1015 step 1/2 (+0s): 9.973µs self time
                                  Task#1015 step 2/2 (+9.973µs): return nil
                                  Task#1015 ends at 18.955µs
                                    Gather#1015: index=2
                                    Gather#1015 step 1/12 (+0s): 21ns self time
                                    Gather#1015 step 2/12 (+21ns): scatter:
                                      Task#1010: pool=0
                                      Task#1010 step 1/2 (+0s): 9.699µs self time
                                      Task#1010 step 2/2 (+9.699µs): return nil
                                      Task#1010 ends at 28.675µs
                                        Gather#1010: index=0
                                        Gather#1010 step 1/2 (+0s): 281ns self time
                                        Gather#1010 step 2/2 (+281ns): return nil
                                        Gather#1010 ends at 28.956µs
                                    Gather#1015 step 3/12 (+21ns): 3ns self time
                                    Gather#1015 step 4/12 (+24ns): scatter:
                                      Task#1007: pool=2
                                      Task#1007 step 1/2 (+0s): 10.003µs self time
                                      Task#1007 step 2/2 (+10.003µs): return nil
                                      Task#1007 ends at 28.982µs
                                        Combine#1007: index=7 flush=<nil>
                                        Combine#1007 step 1/2 (+0s): 996ns self time
                                        Combine#1007 step 2/2 (+996ns): return nil
                                        Combine#1007 ends at 29.978µs
                                    Gather#1015 step 5/12 (+24ns): 31ns self time
                                    Gather#1015 step 6/12 (+55ns): scatter:
                                      Task#1014: pool=1
                                      Task#1014 step 1/2 (+0s): 10.511µs self time
                                      Task#1014 step 2/2 (+10.511µs): return nil
                                      Task#1014 ends at 29.521µs
                                        Gather#1014: index=1
                                        Gather#1014 step 1/4 (+0s): 482ns self time
                                        Gather#1014 step 2/4 (+482ns): scatter:
                                          Task#1003: pool=1
                                          Task#1003 step 1/2 (+0s): 10.018µs self time
                                          Task#1003 step 2/2 (+10.018µs): return nil
                                          Task#1003 ends at 40.021µs
                                            Combine#1003: index=5 flush=<nil>
                                            Combine#1003 step 1/2 (+0s): 986ns self time
                                            Combine#1003 step 2/2 (+986ns): return nil
                                            Combine#1003 ends at 41.007µs
                                        Gather#1014 step 3/4 (+482ns): 519ns self time
                                        Gather#1014 step 4/4 (+1.001µs): return nil
                                        Gather#1014 ends at 30.522µs
                                    Gather#1015 step 7/12 (+55ns): 28ns self time
                                    Gather#1015 step 8/12 (+83ns): scatter:
                                      Task#1013: pool=1
                                      Task#1013 step 1/2 (+0s): 10.027µs self time
                                      Task#1013 step 2/2 (+10.027µs): return nil
                                      Task#1013 ends at 29.065µs
                                        Combine#1013: index=5 flush=<nil>
                                        Combine#1013 step 1/4 (+0s): 4.339µs self time
                                        Combine#1013 step 2/4 (+4.339µs): scatter:
                                          Task#1002: pool=3
                                          Task#1002 step 1/2 (+0s): 9.998µs self time
                                          Task#1002 step 2/2 (+9.998µs): return nil
                                          Task#1002 ends at 43.402µs
                                            Gather#1002: index=2
                                            Gather#1002 step 1/2 (+0s): 999ns self time
                                            Gather#1002 step 2/2 (+999ns): return nil
                                            Gather#1002 ends at 44.401µs
                                        Combine#1013 step 3/4 (+4.339µs): 4.322µs self time
                                        Combine#1013 step 4/4 (+8.661µs): return nil
                                        Combine#1013 ends at 37.726µs
                                    Gather#1015 step 9/12 (+83ns): 34ns self time
                                    Gather#1015 step 10/12 (+117ns): scatter:
                                      Task#1009: pool=3
                                      Task#1009 step 1/2 (+0s): 10.003µs self time
                                      Task#1009 step 2/2 (+10.003µs): return nil
                                      Task#1009 ends at 29.075µs
                                        Gather#1009: index=0
                                        Gather#1009 step 1/2 (+0s): 1.008µs self time
                                        Gather#1009 step 2/2 (+1.008µs): return nil
                                        Gather#1009 ends at 30.083µs
                                    Gather#1015 step 11/12 (+117ns): 33ns self time
                                    Gather#1015 step 12/12 (+150ns): return error
                                    Gather#1015 ends at 19.105µs
                                Gather#1016 step 3/10 (+60ns): 214ns self time
                                Gather#1016 step 4/10 (+274ns): scatter:
                                  Task#999: pool=0
                                  Task#999 step 1/2 (+0s): 10µs self time
                                  Task#999 step 2/2 (+10µs): return nil
                                  Task#999 ends at 19.196µs
                                    Gather#999: index=1
                                    Gather#999 step 1/2 (+0s): 764ns self time
                                    Gather#999 step 2/2 (+764ns): return nil
                                    Gather#999 ends at 19.96µs
                                Gather#1016 step 5/10 (+274ns): 34ns self time
                                Gather#1016 step 6/10 (+308ns): scatter:
                                  Task#1001: pool=4
                                  Task#1001 step 1/2 (+0s): 10µs self time
                                  Task#1001 step 2/2 (+10µs): return nil
                                  Task#1001 ends at 19.23µs
                                    Combine#1001: index=2 flush=<nil>
                                    Combine#1001 step 1/2 (+0s): 1.02µs self time
                                    Combine#1001 step 2/2 (+1.02µs): return nil
                                    Combine#1001 ends at 20.25µs
                                Gather#1016 step 7/10 (+308ns): 409ns self time
                                Gather#1016 step 8/10 (+717ns): scatter:
                                  Task#1006: pool=0
                                  Task#1006 step 1/2 (+0s): 9.997µs self time
                                  Task#1006 step 2/2 (+9.997µs): return nil
                                  Task#1006 ends at 19.636µs
                                    Gather#1006: index=0
                                    Gather#1006 step 1/2 (+0s): 995ns self time
                                    Gather#1006 step 2/2 (+995ns): return nil
                                    Gather#1006 ends at 20.631µs
                                Gather#1016 step 9/10 (+717ns): 387ns self time
                                Gather#1016 step 10/10 (+1.104µs): return nil
                                Gather#1016 ends at 10.026µs
                            Plan#37 step 2/9 (+0s): scatter:
                              Task#998: pool=1
                              Task#998 step 1/2 (+0s): 10µs self time
                              Task#998 step 2/2 (+10µs): return nil
                              Task#998 ends at 10µs
                                Gather#998: index=1
                                Gather#998 step 1/2 (+0s): 998ns self time
                                Gather#998 step 2/2 (+998ns): return nil
                                Gather#998 ends at 10.998µs
                            Plan#37 step 3/9 (+0s): scatter:
                              Task#1000: pool=4
                              Task#1000 step 1/2 (+0s): 4.71µs self time
                              Task#1000 step 2/2 (+4.71µs): return nil
                              Task#1000 ends at 4.71µs
                                Combine#1000: index=6 flush=<nil>
                                Combine#1000 step 1/2 (+0s): 2.472µs self time
                                Combine#1000 step 2/2 (+2.472µs): return nil
                                Combine#1000 ends at 7.182µs
                            Plan#37 step 4/9 (+0s): scatter:
                              Task#1008: pool=1
                              Task#1008 step 1/2 (+0s): 9.986µs self time
                              Task#1008 step 2/2 (+9.986µs): return nil
                              Task#1008 ends at 9.986µs
                                Gather#1008: index=2
                                Gather#1008 step 1/2 (+0s): 996ns self time
                                Gather#1008 step 2/2 (+996ns): return nil
                                Gather#1008 ends at 10.982µs
                            Plan#37 step 5/9 (+0s): scatter:
                              Task#1012: pool=1
                              Task#1012 step 1/2 (+0s): 8.131µs self time
                              Task#1012 step 2/2 (+8.131µs): return nil
                              Task#1012 ends at 8.131µs
                                Gather#1012: index=1
                                Gather#1012 step 1/2 (+0s): 999ns self time
                                Gather#1012 step 2/2 (+999ns): return nil
                                Gather#1012 ends at 9.13µs
                            Plan#37 step 6/9 (+0s): scatter:
                              Task#1004: pool=0
                              Task#1004 step 1/2 (+0s): 9.996µs self time
                              Task#1004 step 2/2 (+9.996µs): return error
                              Task#1004 ends at 9.996µs
                                Gather#1004: index=0
                                Gather#1004 step 1/2 (+0s): 999ns self time
                                Gather#1004 step 2/2 (+999ns): return nil
                                Gather#1004 ends at 10.995µs
                            Plan#37 step 7/9 (+0s): scatter:
                              Task#1005: pool=4
                              Task#1005 step 1/2 (+0s): 10.019µs self time
                              Task#1005 step 2/2 (+10.019µs): return nil
                              Task#1005 ends at 10.019µs
                                Gather#1005: index=1
                                Gather#1005 step 1/2 (+0s): 1.08µs self time
                                Gather#1005 step 2/2 (+1.08µs): return nil
                                Gather#1005 ends at 11.099µs
                            Plan#37 step 8/9 (+0s): scatter:
                              Task#1011: pool=0
                              Task#1011 step 1/2 (+0s): 9.985µs self time
                              Task#1011 step 2/2 (+9.985µs): return nil
                              Task#1011 ends at 9.985µs
                                Gather#1011: index=2
                                Gather#1011 step 1/2 (+0s): 987ns self time
                                Gather#1011 step 2/2 (+987ns): return nil
                                Gather#1011 ends at 10.972µs
                            Plan#37 step 9/9 (+0s): ends at 44.401µs
                          Gather#997 step 3/4 (+44.888µs): 469ns self time
                          Gather#997 step 4/4 (+45.357µs): return nil
                          Gather#997 ends at 8.628848ms
                      Combine#1053 step 3/4 (+604.968µs): 324.158µs self time
                      Combine#1053 step 4/4 (+929.126µs): return nil
                      Combine#1053 ends at 8.897649ms
                  Plan#36 step 2/5 (+0s): scatter:
                    Task#1052: pool=1
                    Task#1052 step 1/2 (+0s): 10.061µs self time
                    Task#1052 step 2/2 (+10.061µs): return nil
                    Task#1052 ends at 10.061µs
                      Gather#1052: index=0
                      Gather#1052 step 1/4 (+0s): 244ns self time
                      Gather#1052 step 2/4 (+244ns): scatter:
                        Task#994: pool=1
                        Task#994 step 1/2 (+0s): 10.034µs self time
                        Task#994 step 2/2 (+10.034µs): return nil
                        Task#994 ends at 20.339µs
                          Gather#994: index=0
                          Gather#994 step 1/2 (+0s): 1µs self time
                          Gather#994 step 2/2 (+1µs): return nil
                          Gather#994 ends at 21.339µs
                      Gather#1052 step 3/4 (+244ns): 295ns self time
                      Gather#1052 step 4/4 (+539ns): return error
                      Gather#1052 ends at 10.6µs
                  Plan#36 step 3/5 (+0s): scatter:
                    Task#1021: pool=3
                    Task#1021 step 1/4 (+0s): 4.944µs self time
                    Task#1021 step 2/4 (+4.944µs): subjob:
                      Plan#38: pathCount=22 taskCount=30 maxPathDuration=9.344714ms minGatherCount=21 maxGatherCount=39
                         TaskPools[0]: TaskPool#128: limit=8
                         TaskPools[1]: TaskPool#129: limit=3
                         TaskPools[2]: TaskPool#130: limit=10
                         TaskPools[3]: TaskPool#131: limit=1
                         TaskPools[4]: TaskPool#132: limit=1
                         TaskPools[5]: TaskPool#133: limit=1
                         TaskPools[6]: TaskPool#134: limit=1
                         TaskPools[7]: TaskPool#135: limit=1
                         TaskPools[8]: TaskPool#136: limit=4
                         CombinerPools[0]: CombinerPool#164: limit=2
                         Combiners[0]: pool=0
                         Combiners[1]: pool=0
                         Combiners[2]: pool=0
                         Combiners[3]: pool=0
                      Plan#38 step 1/8 (+0s): scatter:
                        Task#1051: pool=0
                        Task#1051 step 1/2 (+0s): 9.998µs self time
                        Task#1051 step 2/2 (+9.998µs): return error
                        Task#1051 ends at 9.998µs
                          Combine#1051: index=3 flush=<nil>
                          Combine#1051 step 1/18 (+0s): 35ns self time
                          Combine#1051 step 2/18 (+35ns): scatter:
                            Task#1047: pool=4
                            Task#1047 step 1/2 (+0s): 833.724µs self time
                            Task#1047 step 2/2 (+833.724µs): return nil
                            Task#1047 ends at 843.757µs
                              Gather#1047: index=0
                              Gather#1047 step 1/6 (+0s): 335ns self time
                              Gather#1047 step 2/6 (+335ns): scatter:
                                Task#1045: pool=0
                                Task#1045 step 1/2 (+0s): 9.997µs self time
                                Task#1045 step 2/2 (+9.997µs): return nil
                                Task#1045 ends at 854.089µs
                                  Gather#1045: index=0
                                  Gather#1045 step 1/14 (+0s): 1.255µs self time
                                  Gather#1045 step 2/14 (+1.255µs): scatter:
                                    Task#1034: pool=6
                                    Task#1034 step 1/2 (+0s): 9.999µs self time
                                    Task#1034 step 2/2 (+9.999µs): return nil
                                    Task#1034 ends at 865.343µs
                                      Gather#1034: index=1
                                      Gather#1034 step 1/2 (+0s): 1.822µs self time
                                      Gather#1034 step 2/2 (+1.822µs): return nil
                                      Gather#1034 ends at 867.165µs
                                  Gather#1045 step 3/14 (+1.255µs): 2.501µs self time
                                  Gather#1045 step 4/14 (+3.756µs): scatter:
                                    Task#1031: pool=1
                                    Task#1031 step 1/2 (+0s): 10.003µs self time
                                    Task#1031 step 2/2 (+10.003µs): return nil
                                    Task#1031 ends at 867.848µs
                                      Gather#1031: index=0
                                      Gather#1031 step 1/2 (+0s): 746ns self time
                                      Gather#1031 step 2/2 (+746ns): return nil
                                      Gather#1031 ends at 868.594µs
                                  Gather#1045 step 5/14 (+3.756µs): 1.07µs self time
                                  Gather#1045 step 6/14 (+4.826µs): scatter:
                                    Task#1044: pool=1
                                    Task#1044 step 1/2 (+0s): 8.470841ms self time
                                    Task#1044 step 2/2 (+8.470841ms): return nil
                                    Task#1044 ends at 9.329756ms
                                      Gather#1044: index=1
                                      Gather#1044 step 1/4 (+0s): 725ns self time
                                      Gather#1044 step 2/4 (+725ns): scatter:
                                        Task#1040: pool=0
                                        Task#1040 step 1/2 (+0s): 9.998µs self time
                                        Task#1040 step 2/2 (+9.998µs): return nil
                                        Task#1040 ends at 9.340479ms
                                          Combine#1040: index=1 flush=<nil>
                                          Combine#1040 step 1/2 (+0s): 4.235µs self time
                                          Combine#1040 step 2/2 (+4.235µs): return nil
                                          Combine#1040 ends at 9.344714ms
                                      Gather#1044 step 3/4 (+725ns): 229ns self time
                                      Gather#1044 step 4/4 (+954ns): return nil
                                      Gather#1044 ends at 9.33071ms
                                  Gather#1045 step 7/14 (+4.826µs): 0s self time
                                  Gather#1045 step 8/14 (+4.826µs): scatter:
                                    Task#1035: pool=8
                                    Task#1035 step 1/2 (+0s): 12.627µs self time
                                    Task#1035 step 2/2 (+12.627µs): return nil
                                    Task#1035 ends at 871.542µs
                                      Gather#1035: index=0
                                      Gather#1035 step 1/2 (+0s): 396ns self time
                                      Gather#1035 step 2/2 (+396ns): return nil
                                      Gather#1035 ends at 871.938µs
                                  Gather#1045 step 9/14 (+4.826µs): 1.348µs self time
                                  Gather#1045 step 10/14 (+6.174µs): scatter:
                                    Task#1024: pool=6
                                    Task#1024 step 1/2 (+0s): 10.002µs self time
                                    Task#1024 step 2/2 (+10.002µs): return nil
                                    Task#1024 ends at 870.265µs
                                      Combine#1024: index=1 flush=<nil>
                                      Combine#1024 step 1/2 (+0s): 175ns self time
                                      Combine#1024 step 2/2 (+175ns): return nil
                                      Combine#1024 ends at 870.44µs
                                  Gather#1045 step 11/14 (+6.174µs): 1.347µs self time
                                  Gather#1045 step 12/14 (+7.521µs): scatter:
                                    Task#1028: pool=0
                                    Task#1028 step 1/2 (+0s): 13.863µs self time
                                    Task#1028 step 2/2 (+13.863µs): return nil
                                    Task#1028 ends at 875.473µs
                                      Gather#1028: index=0
                                      Gather#1028 step 1/2 (+0s): 999ns self time
                                      Gather#1028 step 2/2 (+999ns): return nil
                                      Gather#1028 ends at 876.472µs
                                  Gather#1045 step 13/14 (+7.521µs): 1.308µs self time
                                  Gather#1045 step 14/14 (+8.829µs): return nil
                                  Gather#1045 ends at 862.918µs
                              Gather#1047 step 3/6 (+335ns): 331ns self time
                              Gather#1047 step 4/6 (+666ns): scatter:
                                Task#1042: pool=5
                                Task#1042 step 1/2 (+0s): 9.993µs self time
                                Task#1042 step 2/2 (+9.993µs): return nil
                                Task#1042 ends at 854.416µs
                                  Combine#1042: index=2 flush=<nil>
                                  Combine#1042 step 1/2 (+0s): 441ns self time
                                  Combine#1042 step 2/2 (+441ns): return nil
                                  Combine#1042 ends at 854.857µs
                              Gather#1047 step 5/6 (+666ns): 331ns self time
                              Gather#1047 step 6/6 (+997ns): return nil
                              Gather#1047 ends at 844.754µs
                          Combine#1051 step 3/18 (+35ns): 9ns self time
                          Combine#1051 step 4/18 (+44ns): scatter:
                            Task#1022: pool=3
                            Task#1022 step 1/2 (+0s): 632.248µs self time
                            Task#1022 step 2/2 (+632.248µs): return nil
                            Task#1022 ends at 642.29µs
                              Gather#1022: index=0
                              Gather#1022 step 1/2 (+0s): 1.009µs self time
                              Gather#1022 step 2/2 (+1.009µs): return nil
                              Gather#1022 ends at 643.299µs
                          Combine#1051 step 5/18 (+44ns): 258ns self time
                          Combine#1051 step 6/18 (+302ns): scatter:
                            Task#1041: pool=3
                            Task#1041 step 1/2 (+0s): 3.179µs self time
                            Task#1041 step 2/2 (+3.179µs): return nil
                            Task#1041 ends at 13.479µs
                              Combine#1041: index=1 flush=<nil>
                              Combine#1041 step 1/2 (+0s): 1.345µs self time
                              Combine#1041 step 2/2 (+1.345µs): return nil
                              Combine#1041 ends at 14.824µs
                          Combine#1051 step 7/18 (+302ns): 0s self time
                          Combine#1051 step 8/18 (+302ns): scatter:
                            Task#1043: pool=6
                            Task#1043 step 1/2 (+0s): 10.003µs self time
                            Task#1043 step 2/2 (+10.003µs): return nil
                            Task#1043 ends at 20.303µs
                              Gather#1043: index=0
                              Gather#1043 step 1/2 (+0s): 999ns self time
                              Gather#1043 step 2/2 (+999ns): return nil
                              Gather#1043 ends at 21.302µs
                          Combine#1051 step 9/18 (+302ns): 0s self time
                          Combine#1051 step 10/18 (+302ns): scatter:
                            Task#1048: pool=2
                            Task#1048 step 1/2 (+0s): 4.406µs self time
                            Task#1048 step 2/2 (+4.406µs): return nil
                            Task#1048 ends at 14.706µs
                              Gather#1048: index=1
                              Gather#1048 step 1/4 (+0s): 405ns self time
                              Gather#1048 step 2/4 (+405ns): scatter:
                                Task#1025: pool=8
                                Task#1025 step 1/2 (+0s): 6.177µs self time
                                Task#1025 step 2/2 (+6.177µs): return nil
                                Task#1025 ends at 21.288µs
                                  Gather#1025: index=0
                                  Gather#1025 step 1/2 (+0s): 994ns self time
                                  Gather#1025 step 2/2 (+994ns): return nil
                                  Gather#1025 ends at 22.282µs
                              Gather#1048 step 3/4 (+405ns): 595ns self time
                              Gather#1048 step 4/4 (+1µs): return nil
                              Gather#1048 ends at 15.706µs
                          Combine#1051 step 11/18 (+302ns): 0s self time
                          Combine#1051 step 12/18 (+302ns): scatter:
                            Task#1049: pool=2
                            Task#1049 step 1/2 (+0s): 10µs self time
                            Task#1049 step 2/2 (+10µs): return nil
                            Task#1049 ends at 20.3µs
                              Gather#1049: index=0
                              Gather#1049 step 1/8 (+0s): 250ns self time
                              Gather#1049 step 2/8 (+250ns): scatter:
                                Task#1046: pool=3
                                Task#1046 step 1/2 (+0s): 10.004µs self time
                                Task#1046 step 2/2 (+10.004µs): return nil
                                Task#1046 ends at 30.554µs
                                  Gather#1046: index=1
                                  Gather#1046 step 1/4 (+0s): 498ns self time
                                  Gather#1046 step 2/4 (+498ns): scatter:
                                    Task#1027: pool=0
                                    Task#1027 step 1/2 (+0s): 2.831µs self time
                                    Task#1027 step 2/2 (+2.831µs): return nil
                                    Task#1027 ends at 33.883µs
                                      Gather#1027: index=0
                                      Gather#1027 step 1/2 (+0s): 1µs self time
                                      Gather#1027 step 2/2 (+1µs): return nil
                                      Gather#1027 ends at 34.883µs
                                  Gather#1046 step 3/4 (+498ns): 502ns self time
                                  Gather#1046 step 4/4 (+1µs): return nil
                                  Gather#1046 ends at 31.554µs
                              Gather#1049 step 3/8 (+250ns): 153ns self time
                              Gather#1049 step 4/8 (+403ns): scatter:
                                Task#1023: pool=8
                                Task#1023 step 1/2 (+0s): 45.089µs self time
                                Task#1023 step 2/2 (+45.089µs): return error
                                Task#1023 ends at 65.792µs
                                  Gather#1023: index=1
                                  Gather#1023 step 1/2 (+0s): 1.012µs self time
                                  Gather#1023 step 2/2 (+1.012µs): return nil
                                  Gather#1023 ends at 66.804µs
                              Gather#1049 step 5/8 (+403ns): 273ns self time
                              Gather#1049 step 6/8 (+676ns): scatter:
                                Task#1036: pool=8
                                Task#1036 step 1/2 (+0s): 35.62µs self time
                                Task#1036 step 2/2 (+35.62µs): return nil
                                Task#1036 ends at 56.596µs
                                  Combine#1036: index=2 flush=<nil>
                                  Combine#1036 step 1/2 (+0s): 1µs self time
                                  Combine#1036 step 2/2 (+1µs): return nil
                                  Combine#1036 ends at 57.596µs
                              Gather#1049 step 7/8 (+676ns): 274ns self time
                              Gather#1049 step 8/8 (+950ns): return nil
                              Gather#1049 ends at 21.25µs
                          Combine#1051 step 13/18 (+302ns): 1ns self time
                          Combine#1051 step 14/18 (+303ns): scatter:
                            Task#1039: pool=5
                            Task#1039 step 1/2 (+0s): 10.068µs self time
                            Task#1039 step 2/2 (+10.068µs): return nil
                            Task#1039 ends at 20.369µs
                              Gather#1039: index=1
                              Gather#1039 step 1/2 (+0s): 590ns self time
                              Gather#1039 step 2/2 (+590ns): return error
                              Gather#1039 ends at 20.959µs
                          Combine#1051 step 15/18 (+303ns): 5ns self time
                          Combine#1051 step 16/18 (+308ns): scatter:
                            Task#1030: pool=1
                            Task#1030 step 1/2 (+0s): 10.008µs self time
                            Task#1030 step 2/2 (+10.008µs): return error
                            Task#1030 ends at 20.314µs
                              Combine#1030: index=0 flush=<nil>
                              Combine#1030 step 1/2 (+0s): 977ns self time
                              Combine#1030 step 2/2 (+977ns): return nil
                              Combine#1030 ends at 21.291µs
                          Combine#1051 step 17/18 (+308ns): 4ns self time
                          Combine#1051 step 18/18 (+312ns): return nil
                          Combine#1051 ends at 10.31µs
                      Plan#38 step 2/8 (+0s): scatter:
                        Task#1050: pool=6
                        Task#1050 step 1/2 (+0s): 7.352µs self time
                        Task#1050 step 2/2 (+7.352µs): return nil
                        Task#1050 ends at 7.352µs
                          Gather#1050: index=0
                          Gather#1050 step 1/4 (+0s): 523ns self time
                          Gather#1050 step 2/4 (+523ns): scatter:
                            Task#1037: pool=2
                            Task#1037 step 1/2 (+0s): 979ns self time
                            Task#1037 step 2/2 (+979ns): return nil
                            Task#1037 ends at 8.854µs
                              Gather#1037: index=0
                              Gather#1037 step 1/2 (+0s): 1.608µs self time
                              Gather#1037 step 2/2 (+1.608µs): return nil
                              Gather#1037 ends at 10.462µs
                          Gather#1050 step 3/4 (+523ns): 513ns self time
                          Gather#1050 step 4/4 (+1.036µs): return nil
                          Gather#1050 ends at 8.388µs
                      Plan#38 step 3/8 (+0s): scatter:
                        Task#1029: pool=4
                        Task#1029 step 1/2 (+0s): 10.675µs self time
                        Task#1029 step 2/2 (+10.675µs): return nil
                        Task#1029 ends at 10.675µs
                          Combine#1029: index=2 flush=<nil>
                          Combine#1029 step 1/2 (+0s): 65.215µs self time
                          Combine#1029 step 2/2 (+65.215µs): return nil
                          Combine#1029 ends at 75.89µs
                      Plan#38 step 4/8 (+0s): scatter:
                        Task#1033: pool=1
                        Task#1033 step 1/2 (+0s): 10µs self time
                        Task#1033 step 2/2 (+10µs): return nil
                        Task#1033 ends at 10µs
                          Gather#1033: index=1
                          Gather#1033 step 1/2 (+0s): 360.524µs self time
                          Gather#1033 step 2/2 (+360.524µs): return nil
                          Gather#1033 ends at 370.524µs
                      Plan#38 step 5/8 (+0s): scatter:
                        Task#1032: pool=1
                        Task#1032 step 1/2 (+0s): 13.01µs self time
                        Task#1032 step 2/2 (+13.01µs): return nil
                        Task#1032 ends at 13.01µs
                          Gather#1032: index=0
                          Gather#1032 step 1/2 (+0s): 1.011µs self time
                          Gather#1032 step 2/2 (+1.011µs): return nil
                          Gather#1032 ends at 14.021µs
                      Plan#38 step 6/8 (+0s): scatter:
                        Task#1038: pool=6
                        Task#1038 step 1/2 (+0s): 236ns self time
                        Task#1038 step 2/2 (+236ns): return nil
                        Task#1038 ends at 236ns
                          Gather#1038: index=0
                          Gather#1038 step 1/2 (+0s): 1.001µs self time
                          Gather#1038 step 2/2 (+1.001µs): return nil
                          Gather#1038 ends at 1.237µs
                      Plan#38 step 7/8 (+0s): scatter:
                        Task#1026: pool=7
                        Task#1026 step 1/2 (+0s): 10.003µs self time
                        Task#1026 step 2/2 (+10.003µs): return nil
                        Task#1026 ends at 10.003µs
                          Combine#1026: index=0 flush=<nil>
                          Combine#1026 step 1/2 (+0s): 408ns self time
                          Combine#1026 step 2/2 (+408ns): return nil
                          Combine#1026 ends at 10.411µs
                      Plan#38 step 8/8 (+0s): ends at 9.344714ms
                    Task#1021 step 3/4 (+9.349658ms): 4.942µs self time
                    Task#1021 step 4/4 (+9.3546ms): return nil
                    Task#1021 ends at 9.3546ms
                      Combine#1021: index=2 flush=<nil>
                      Combine#1021 step 1/6 (+0s): 0s self time
                      Combine#1021 step 2/6 (+0s): scatter:
                        Task#1019: pool=0
                        Task#1019 step 1/2 (+0s): 9.639µs self time
                        Task#1019 step 2/2 (+9.639µs): return nil
                        Task#1019 ends at 9.364239ms
                          Gather#1019: index=0
                          Gather#1019 step 1/4 (+0s): 510ns self time
                          Gather#1019 step 2/4 (+510ns): scatter:
                            Task#996: pool=1
                            Task#996 step 1/2 (+0s): 3.344µs self time
                            Task#996 step 2/2 (+3.344µs): return error
                            Task#996 ends at 9.368093ms
                              Gather#996: index=0
                              Gather#996 step 1/2 (+0s): 1.014µs self time
                              Gather#996 step 2/2 (+1.014µs): return nil
                              Gather#996 ends at 9.369107ms
                          Gather#1019 step 3/4 (+510ns): 482ns self time
                          Gather#1019 step 4/4 (+992ns): return nil
                          Gather#1019 ends at 9.365231ms
                      Combine#1021 step 3/6 (+0s): 3.378µs self time
                      Combine#1021 step 4/6 (+3.378µs): scatter:
                        Task#993: pool=0
                        Task#993 step 1/2 (+0s): 6.467606ms self time
                        Task#993 step 2/2 (+6.467606ms): return nil
                        Task#993 ends at 15.825584ms
                          Gather#993: index=0
                          Gather#993 step 1/2 (+0s): 729ns self time
                          Gather#993 step 2/2 (+729ns): return nil
                          Gather#993 ends at 15.826313ms
                      Combine#1021 step 5/6 (+3.378µs): 9.1µs self time
                      Combine#1021 step 6/6 (+12.478µs): return nil
                      Combine#1021 ends at 9.367078ms
                  Plan#36 step 4/5 (+0s): scatter:
                    Task#1020: pool=4
                    Task#1020 step 1/2 (+0s): 30ns self time
                    Task#1020 step 2/2 (+30ns): return nil
                    Task#1020 ends at 30ns
                      Combine#1020: index=2 flush=<nil>
                      Combine#1020 step 1/4 (+0s): 529ns self time
                      Combine#1020 step 2/4 (+529ns): scatter:
                        Task#1018: pool=0
                        Task#1018 step 1/2 (+0s): 9.99µs self time
                        Task#1018 step 2/2 (+9.99µs): return nil
                        Task#1018 ends at 10.549µs
                          Combine#1018: index=3 flush=<nil>
                          Combine#1018 step 1/4 (+0s): 482ns self time
                          Combine#1018 step 2/4 (+482ns): scatter:
                            Task#1017: pool=4
                            Task#1017 step 1/2 (+0s): 9.993µs self time
                            Task#1017 step 2/2 (+9.993µs): return nil
                            Task#1017 ends at 21.024µs
                              Combine#1017: index=2 flush=<nil>
                              Combine#1017 step 1/4 (+0s): 23ns self time
                              Combine#1017 step 2/4 (+23ns): scatter:
                                Task#995: pool=4
                                Task#995 step 1/2 (+0s): 2.872322ms self time
                                Task#995 step 2/2 (+2.872322ms): return nil
                                Task#995 ends at 2.893369ms
                                  Gather#995: index=0
                                  Gather#995 step 1/2 (+0s): 1.015µs self time
                                  Gather#995 step 2/2 (+1.015µs): return nil
                                  Gather#995 ends at 2.894384ms
                              Combine#1017 step 3/4 (+23ns): 975ns self time
                              Combine#1017 step 4/4 (+998ns): return nil
                              Combine#1017 ends at 22.022µs
                          Combine#1018 step 3/4 (+482ns): 519ns self time
                          Combine#1018 step 4/4 (+1.001µs): return error
                          Combine#1018 ends at 11.55µs
                      Combine#1020 step 3/4 (+529ns): 522ns self time
                      Combine#1020 step 4/4 (+1.051µs): return nil
                      Combine#1020 ends at 1.081µs
                  Plan#36 step 5/5 (+0s): ends at 15.826313ms
                Task#900 step 3/4 (+15.826313ms): 0s self time
                Task#900 step 4/4 (+15.826313ms): return nil
                Task#900 ends at 15.826313ms
                  Gather#900: index=3
                  Gather#900 step 1/10 (+0s): 156ns self time
                  Gather#900 step 2/10 (+156ns): scatter:
                    Task#896: pool=0
                    Task#896 step 1/2 (+0s): 473.722µs self time
                    Task#896 step 2/2 (+473.722µs): return nil
                    Task#896 ends at 16.300191ms
                      Gather#896: index=0
                      Gather#896 step 1/4 (+0s): 0s self time
                      Gather#896 step 2/4 (+0s): scatter:
                        Task#699: pool=0
                        Task#699 step 1/2 (+0s): 3.35µs self time
                        Task#699 step 2/2 (+3.35µs): return nil
                        Task#699 ends at 16.303541ms
                          Gather#699: index=3
                          Gather#699 step 1/4 (+0s): 356ns self time
                          Gather#699 step 2/4 (+356ns): scatter:
                            Task#272: pool=0
                            Task#272 step 1/2 (+0s): 10µs self time
                            Task#272 step 2/2 (+10µs): return nil
                            Task#272 ends at 16.313897ms
                              Gather#272: index=0
                              Gather#272 step 1/2 (+0s): 1.131µs self time
                              Gather#272 step 2/2 (+1.131µs): return nil
                              Gather#272 ends at 16.315028ms
                          Gather#699 step 3/4 (+356ns): 647ns self time
                          Gather#699 step 4/4 (+1.003µs): return nil
                          Gather#699 ends at 16.304544ms
                      Gather#896 step 3/4 (+0s): 0s self time
                      Gather#896 step 4/4 (+0s): return nil
                      Gather#896 ends at 16.300191ms
                  Gather#900 step 3/10 (+156ns): 163ns self time
                  Gather#900 step 4/10 (+319ns): scatter:
                    Task#702: pool=0
                    Task#702 step 1/2 (+0s): 9.802µs self time
                    Task#702 step 2/2 (+9.802µs): return nil
                    Task#702 ends at 15.836434ms
                      Gather#702: index=0
                      Gather#702 step 1/4 (+0s): 496ns self time
                      Gather#702 step 2/4 (+496ns): scatter:
                        Task#282: pool=0
                        Task#282 step 1/2 (+0s): 9.998µs self time
                        Task#282 step 2/2 (+9.998µs): return nil
                        Task#282 ends at 15.846928ms
                          Gather#282: index=3
                          Gather#282 step 1/2 (+0s): 999ns self time
                          Gather#282 step 2/2 (+999ns): return nil
                          Gather#282 ends at 15.847927ms
                      Gather#702 step 3/4 (+496ns): 500ns self time
                      Gather#702 step 4/4 (+996ns): return nil
                      Gather#702 ends at 15.83743ms
                  Gather#900 step 5/10 (+319ns): 172ns self time
                  Gather#900 step 6/10 (+491ns): subjob:
                    Plan#33: pathCount=15 taskCount=23 maxPathDuration=19.152323ms minGatherCount=19 maxGatherCount=23
                       TaskPools[0]: TaskPool#101: limit=1
                       TaskPools[1]: TaskPool#102: limit=2
                       TaskPools[2]: TaskPool#103: limit=2
                       TaskPools[3]: TaskPool#104: limit=10
                       TaskPools[4]: TaskPool#105: limit=4
                       TaskPools[5]: TaskPool#106: limit=2
                       TaskPools[6]: TaskPool#107: limit=10
                       TaskPools[7]: TaskPool#108: limit=4
                       TaskPools[8]: TaskPool#109: limit=7
                       TaskPools[9]: TaskPool#110: limit=2
                       CombinerPools[0]: CombinerPool#152: limit=3
                       CombinerPools[1]: CombinerPool#153: limit=2
                       CombinerPools[2]: CombinerPool#154: limit=1
                       Combiners[0]: pool=2
                       Combiners[1]: pool=2
                       Combiners[2]: pool=2
                       Combiners[3]: pool=1
                       Combiners[4]: pool=0
                       Combiners[5]: pool=0
                       Combiners[6]: pool=2
                       Combiners[7]: pool=2
                       Combiners[8]: pool=2
                       Combiners[9]: pool=1
                       Combiners[10]: pool=0
                       Combiners[11]: pool=0
                       Combiners[12]: pool=2
                    Plan#33 step 1/7 (+0s): scatter:
                      Task#991: pool=2
                      Task#991 step 1/2 (+0s): 9.997µs self time
                      Task#991 step 2/2 (+9.997µs): return nil
                      Task#991 ends at 9.997µs
                        Gather#991: index=3
                        Gather#991 step 1/4 (+0s): 169ns self time
                        Gather#991 step 2/4 (+169ns): scatter:
                          Task#949: pool=8
                          Task#949 step 1/2 (+0s): 10.101µs self time
                          Task#949 step 2/2 (+10.101µs): return nil
                          Task#949 ends at 20.267µs
                            Gather#949: index=4
                            Gather#949 step 1/2 (+0s): 987ns self time
                            Gather#949 step 2/2 (+987ns): return nil
                            Gather#949 ends at 21.254µs
                        Gather#991 step 3/4 (+169ns): 113ns self time
                        Gather#991 step 4/4 (+282ns): return nil
                        Gather#991 ends at 10.279µs
                    Plan#33 step 2/7 (+0s): scatter:
                      Task#990: pool=5
                      Task#990 step 1/2 (+0s): 12.963µs self time
                      Task#990 step 2/2 (+12.963µs): return nil
                      Task#990 ends at 12.963µs
                        Gather#990: index=5
                        Gather#990 step 1/4 (+0s): 492ns self time
                        Gather#990 step 2/4 (+492ns): scatter:
                          Task#904: pool=5
                          Task#904 step 1/2 (+0s): 9.262µs self time
                          Task#904 step 2/2 (+9.262µs): return nil
                          Task#904 ends at 22.717µs
                            Gather#904: index=0
                            Gather#904 step 1/2 (+0s): 1.006µs self time
                            Gather#904 step 2/2 (+1.006µs): return nil
                            Gather#904 ends at 23.723µs
                        Gather#990 step 3/4 (+492ns): 506ns self time
                        Gather#990 step 4/4 (+998ns): return nil
                        Gather#990 ends at 13.961µs
                    Plan#33 step 3/7 (+0s): scatter:
                      Task#948: pool=3
                      Task#948 step 1/2 (+0s): 12.63µs self time
                      Task#948 step 2/2 (+12.63µs): return nil
                      Task#948 ends at 12.63µs
                        Gather#948: index=2
                        Gather#948 step 1/2 (+0s): 1.625µs self time
                        Gather#948 step 2/2 (+1.625µs): return nil
                        Gather#948 ends at 14.255µs
                    Plan#33 step 4/7 (+0s): scatter:
                      Task#955: pool=8
                      Task#955 step 1/2 (+0s): 9.996µs self time
                      Task#955 step 2/2 (+9.996µs): return nil
                      Task#955 ends at 9.996µs
                        Gather#955: index=2
                        Gather#955 step 1/6 (+0s): 240ns self time
                        Gather#955 step 2/6 (+240ns): subjob:
                          Plan#35: pathCount=23 taskCount=33 maxPathDuration=10.135826ms minGatherCount=20 maxGatherCount=46
                             TaskPools[0]: TaskPool#112: limit=1
                             TaskPools[1]: TaskPool#113: limit=3
                             TaskPools[2]: TaskPool#114: limit=2
                             TaskPools[3]: TaskPool#115: limit=1
                             TaskPools[4]: TaskPool#116: limit=5
                             CombinerPools[0]: CombinerPool#157: limit=2
                             Combiners[0]: pool=0
                             Combiners[1]: pool=0
                             Combiners[2]: pool=0
                             Combiners[3]: pool=0
                             Combiners[4]: pool=0
                             Combiners[5]: pool=0
                          Plan#35 step 1/12 (+0s): scatter:
                            Task#958: pool=2
                            Task#958 step 1/2 (+0s): 9.996µs self time
                            Task#958 step 2/2 (+9.996µs): return nil
                            Task#958 ends at 9.996µs
                              Gather#958: index=0
                              Gather#958 step 1/2 (+0s): 4.768µs self time
                              Gather#958 step 2/2 (+4.768µs): return error
                              Gather#958 ends at 14.764µs
                          Plan#35 step 2/12 (+0s): scatter:
                            Task#987: pool=0
                            Task#987 step 1/2 (+0s): 7.285µs self time
                            Task#987 step 2/2 (+7.285µs): return nil
                            Task#987 ends at 7.285µs
                              Gather#987: index=0
                              Gather#987 step 1/4 (+0s): 93.129µs self time
                              Gather#987 step 2/4 (+93.129µs): scatter:
                                Task#972: pool=0
                                Task#972 step 1/2 (+0s): 10.235µs self time
                                Task#972 step 2/2 (+10.235µs): return nil
                                Task#972 ends at 110.649µs
                                  Gather#972: index=1
                                  Gather#972 step 1/2 (+0s): 896ns self time
                                  Gather#972 step 2/2 (+896ns): return nil
                                  Gather#972 ends at 111.545µs
                              Gather#987 step 3/4 (+93.129µs): 93.135µs self time
                              Gather#987 step 4/4 (+186.264µs): return nil
                              Gather#987 ends at 193.549µs
                          Plan#35 step 3/12 (+0s): scatter:
                            Task#962: pool=1
                            Task#962 step 1/2 (+0s): 10µs self time
                            Task#962 step 2/2 (+10µs): return nil
                            Task#962 ends at 10µs
                              Gather#962: index=1
                              Gather#962 step 1/2 (+0s): 995ns self time
                              Gather#962 step 2/2 (+995ns): return nil
                              Gather#962 ends at 10.995µs
                          Plan#35 step 4/12 (+0s): scatter:
                            Task#978: pool=3
                            Task#978 step 1/2 (+0s): 9.79µs self time
                            Task#978 step 2/2 (+9.79µs): return nil
                            Task#978 ends at 9.79µs
                              Gather#978: index=1
                              Gather#978 step 1/2 (+0s): 1.001µs self time
                              Gather#978 step 2/2 (+1.001µs): return error
                              Gather#978 ends at 10.791µs
                          Plan#35 step 5/12 (+0s): scatter:
                            Task#970: pool=1
                            Task#970 step 1/2 (+0s): 11.975µs self time
                            Task#970 step 2/2 (+11.975µs): return nil
                            Task#970 ends at 11.975µs
                              Combine#970: index=0 flush=<nil>
                              Combine#970 step 1/2 (+0s): 942ns self time
                              Combine#970 step 2/2 (+942ns): return nil
                              Combine#970 ends at 12.917µs
                          Plan#35 step 6/12 (+0s): scatter:
                            Task#957: pool=4
                            Task#957 step 1/2 (+0s): 9.988µs self time
                            Task#957 step 2/2 (+9.988µs): return nil
                            Task#957 ends at 9.988µs
                              Gather#957: index=1
                              Gather#957 step 1/2 (+0s): 1.307µs self time
                              Gather#957 step 2/2 (+1.307µs): return error
                              Gather#957 ends at 11.295µs
                          Plan#35 step 7/12 (+0s): scatter:
                            Task#977: pool=2
                            Task#977 step 1/2 (+0s): 9.996µs self time
                            Task#977 step 2/2 (+9.996µs): return nil
                            Task#977 ends at 9.996µs
                              Combine#977: index=4 flush=<nil>
                              Combine#977 step 1/2 (+0s): 603ns self time
                              Combine#977 step 2/2 (+603ns): return nil
                              Combine#977 ends at 10.599µs
                          Plan#35 step 8/12 (+0s): scatter:
                            Task#988: pool=0
                            Task#988 step 1/2 (+0s): 9.999µs self time
                            Task#988 step 2/2 (+9.999µs): return error
                            Task#988 ends at 9.999µs
                              Combine#988: index=4 flush=<nil>
                              Combine#988 step 1/16 (+0s): 23.294µs self time
                              Combine#988 step 2/16 (+23.294µs): scatter:
                                Task#965: pool=0
                                Task#965 step 1/2 (+0s): 9.967µs self time
                                Task#965 step 2/2 (+9.967µs): return nil
                                Task#965 ends at 43.26µs
                                  Combine#965: index=3 flush=<nil>
                                  Combine#965 step 1/2 (+0s): 6.471µs self time
                                  Combine#965 step 2/2 (+6.471µs): return nil
                                  Combine#965 ends at 49.731µs
                              Combine#988 step 3/16 (+23.294µs): 16.25µs self time
                              Combine#988 step 4/16 (+39.544µs): scatter:
                                Task#966: pool=1
                                Task#966 step 1/2 (+0s): 9.992µs self time
                                Task#966 step 2/2 (+9.992µs): return nil
                                Task#966 ends at 59.535µs
                                  Gather#966: index=0
                                  Gather#966 step 1/2 (+0s): 863ns self time
                                  Gather#966 step 2/2 (+863ns): return nil
                                  Gather#966 ends at 60.398µs
                              Combine#988 step 5/16 (+39.544µs): 26.521µs self time
                              Combine#988 step 6/16 (+66.065µs): scatter:
                                Task#974: pool=1
                                Task#974 step 1/2 (+0s): 10.103µs self time
                                Task#974 step 2/2 (+10.103µs): return nil
                                Task#974 ends at 86.167µs
                                  Gather#974: index=0
                                  Gather#974 step 1/2 (+0s): 12.22µs self time
                                  Gather#974 step 2/2 (+12.22µs): return nil
                                  Gather#974 ends at 98.387µs
                              Combine#988 step 7/16 (+66.065µs): 23.879µs self time
                              Combine#988 step 8/16 (+89.944µs): scatter:
                                Task#967: pool=0
                                Task#967 step 1/2 (+0s): 8.068µs self time
                                Task#967 step 2/2 (+8.068µs): return nil
                                Task#967 ends at 108.011µs
                                  Combine#967: index=0 flush=<nil>
                                  Combine#967 step 1/2 (+0s): 1.008µs self time
                                  Combine#967 step 2/2 (+1.008µs): return nil
                                  Combine#967 ends at 109.019µs
                              Combine#988 step 9/16 (+89.944µs): 24.432µs self time
                              Combine#988 step 10/16 (+114.376µs): scatter:
                                Task#984: pool=2
                                Task#984 step 1/2 (+0s): 10.813µs self time
                                Task#984 step 2/2 (+10.813µs): return nil
                                Task#984 ends at 135.188µs
                                  Gather#984: index=0
                                  Gather#984 step 1/6 (+0s): 337ns self time
                                  Gather#984 step 2/6 (+337ns): scatter:
                                    Task#981: pool=3
                                    Task#981 step 1/2 (+0s): 9.989µs self time
                                    Task#981 step 2/2 (+9.989µs): return nil
                                    Task#981 ends at 145.514µs
                                      Gather#981: index=0
                                      Gather#981 step 1/4 (+0s): 159ns self time
                                      Gather#981 step 2/4 (+159ns): scatter:
                                        Task#963: pool=2
                                        Task#963 step 1/2 (+0s): 10.001µs self time
                                        Task#963 step 2/2 (+10.001µs): return nil
                                        Task#963 ends at 155.674µs
                                          Gather#963: index=0
                                          Gather#963 step 1/2 (+0s): 1µs self time
                                          Gather#963 step 2/2 (+1µs): return nil
                                          Gather#963 ends at 156.674µs
                                      Gather#981 step 3/4 (+159ns): 741ns self time
                                      Gather#981 step 4/4 (+900ns): return nil
                                      Gather#981 ends at 146.414µs
                                  Gather#984 step 3/6 (+337ns): 301ns self time
                                  Gather#984 step 4/6 (+638ns): scatter:
                                    Task#961: pool=3
                                    Task#961 step 1/2 (+0s): 10ms self time
                                    Task#961 step 2/2 (+10ms): return nil
                                    Task#961 ends at 10.135826ms
                                      Combine#961: index=0 flush=<nil>
                                      Combine#961 step 1/2 (+0s): 0s self time
                                      Combine#961 step 2/2 (+0s): return nil
                                      Combine#961 ends at 10.135826ms
                                  Gather#984 step 5/6 (+638ns): 364ns self time
                                  Gather#984 step 6/6 (+1.002µs): return nil
                                  Gather#984 ends at 136.19µs
                              Combine#988 step 11/16 (+114.376µs): 29.357µs self time
                              Combine#988 step 12/16 (+143.733µs): scatter:
                                Task#986: pool=2
                                Task#986 step 1/2 (+0s): 9.999µs self time
                                Task#986 step 2/2 (+9.999µs): return nil
                                Task#986 ends at 163.731µs
                                  Combine#986: index=4 flush=<nil>
                                  Combine#986 step 1/4 (+0s): 513ns self time
                                  Combine#986 step 2/4 (+513ns): scatter:
                                    Task#982: pool=0
                                    Task#982 step 1/2 (+0s): 9.999µs self time
                                    Task#982 step 2/2 (+9.999µs): return nil
                                    Task#982 ends at 174.243µs
                                      Combine#982: index=1 flush=<nil>
                                      Combine#982 step 1/4 (+0s): 267ns self time
                                      Combine#982 step 2/4 (+267ns): scatter:
                                        Task#980: pool=4
                                        Task#980 step 1/2 (+0s): 10.002µs self time
                                        Task#980 step 2/2 (+10.002µs): return nil
                                        Task#980 ends at 184.512µs
                                          Gather#980: index=0
                                          Gather#980 step 1/10 (+0s): 158ns self time
                                          Gather#980 step 2/10 (+158ns): scatter:
                                            Task#969: pool=1
                                            Task#969 step 1/2 (+0s): 10.058µs self time
                                            Task#969 step 2/2 (+10.058µs): return nil
                                            Task#969 ends at 194.728µs
                                              Gather#969: index=1
                                              Gather#969 step 1/2 (+0s): 999ns self time
                                              Gather#969 step 2/2 (+999ns): return nil
                                              Gather#969 ends at 195.727µs
                                          Gather#980 step 3/10 (+158ns): 50ns self time
                                          Gather#980 step 4/10 (+208ns): scatter:
                                            Task#959: pool=0
                                            Task#959 step 1/2 (+0s): 9.939µs self time
                                            Task#959 step 2/2 (+9.939µs): return nil
                                            Task#959 ends at 194.659µs
                                              Combine#959: index=2 flush=<nil>
                                              Combine#959 step 1/2 (+0s): 200ns self time
                                              Combine#959 step 2/2 (+200ns): return nil
                                              Combine#959 ends at 194.859µs
                                          Gather#980 step 5/10 (+208ns): 134ns self time
                                          Gather#980 step 6/10 (+342ns): scatter:
                                            Task#964: pool=4
                                            Task#964 step 1/2 (+0s): 9.968µs self time
                                            Task#964 step 2/2 (+9.968µs): return nil
                                            Task#964 ends at 194.822µs
                                              Gather#964: index=1
                                              Gather#964 step 1/2 (+0s): 516.565µs self time
                                              Gather#964 step 2/2 (+516.565µs): return nil
                                              Gather#964 ends at 711.387µs
                                          Gather#980 step 7/10 (+342ns): 338ns self time
                                          Gather#980 step 8/10 (+680ns): scatter:
                                            Task#968: pool=2
                                            Task#968 step 1/2 (+0s): 10.023µs self time
                                            Task#968 step 2/2 (+10.023µs): return nil
                                            Task#968 ends at 195.215µs
                                              Combine#968: index=1 flush=<nil>
                                              Combine#968 step 1/2 (+0s): 1.157µs self time
                                              Combine#968 step 2/2 (+1.157µs): return nil
                                              Combine#968 ends at 196.372µs
                                          Gather#980 step 9/10 (+680ns): 346ns self time
                                          Gather#980 step 10/10 (+1.026µs): return nil
                                          Gather#980 ends at 185.538µs
                                      Combine#982 step 3/4 (+267ns): 246ns self time
                                      Combine#982 step 4/4 (+513ns): return nil
                                      Combine#982 ends at 174.756µs
                                  Combine#986 step 3/4 (+513ns): 479ns self time
                                  Combine#986 step 4/4 (+992ns): return nil
                                  Combine#986 ends at 164.723µs
                              Combine#988 step 13/16 (+143.733µs): 18.595µs self time
                              Combine#988 step 14/16 (+162.328µs): scatter:
                                Task#985: pool=1
                                Task#985 step 1/2 (+0s): 9.562µs self time
                                Task#985 step 2/2 (+9.562µs): return nil
                                Task#985 ends at 181.889µs
                                  Gather#985: index=1
                                  Gather#985 step 1/6 (+0s): 317.407µs self time
                                  Gather#985 step 2/6 (+317.407µs): scatter:
                                    Task#983: pool=3
                                    Task#983 step 1/2 (+0s): 10.02µs self time
                                    Task#983 step 2/2 (+10.02µs): return nil
                                    Task#983 ends at 509.316µs
                                      Gather#983: index=0
                                      Gather#983 step 1/6 (+0s): 41.847µs self time
                                      Gather#983 step 2/6 (+41.847µs): scatter:
                                        Task#979: pool=3
                                        Task#979 step 1/2 (+0s): 10.088µs self time
                                        Task#979 step 2/2 (+10.088µs): return nil
                                        Task#979 ends at 561.251µs
                                          Combine#979: index=5 flush=<nil>
                                          Combine#979 step 1/4 (+0s): 50.028µs self time
                                          Combine#979 step 2/4 (+50.028µs): scatter:
                                            Task#956: pool=4
                                            Task#956 step 1/2 (+0s): 1.523192ms self time
                                            Task#956 step 2/2 (+1.523192ms): return nil
                                            Task#956 ends at 2.134471ms
                                              Gather#956: index=0
                                              Gather#956 step 1/2 (+0s): 1.001µs self time
                                              Gather#956 step 2/2 (+1.001µs): return nil
                                              Gather#956 ends at 2.135472ms
                                          Combine#979 step 3/4 (+50.028µs): 36.664µs self time
                                          Combine#979 step 4/4 (+86.692µs): return nil
                                          Combine#979 ends at 647.943µs
                                      Gather#983 step 3/6 (+41.847µs): 42.429µs self time
                                      Gather#983 step 4/6 (+84.276µs): scatter:
                                        Task#975: pool=0
                                        Task#975 step 1/2 (+0s): 9.984µs self time
                                        Task#975 step 2/2 (+9.984µs): return error
                                        Task#975 ends at 603.576µs
                                          Gather#975: index=0
                                          Gather#975 step 1/2 (+0s): 1.021µs self time
                                          Gather#975 step 2/2 (+1.021µs): return nil
                                          Gather#975 ends at 604.597µs
                                      Gather#983 step 5/6 (+84.276µs): 41.275µs self time
                                      Gather#983 step 6/6 (+125.551µs): return nil
                                      Gather#983 ends at 634.867µs
                                  Gather#985 step 3/6 (+317.407µs): 316.453µs self time
                                  Gather#985 step 4/6 (+633.86µs): scatter:
                                    Task#971: pool=1
                                    Task#971 step 1/2 (+0s): 10.339µs self time
                                    Task#971 step 2/2 (+10.339µs): return nil
                                    Task#971 ends at 826.088µs
                                      Combine#971: index=4 flush=<nil>
                                      Combine#971 step 1/2 (+0s): 1.005µs self time
                                      Combine#971 step 2/2 (+1.005µs): return nil
                                      Combine#971 ends at 827.093µs
                                  Gather#985 step 5/6 (+633.86µs): 316.384µs self time
                                  Gather#985 step 6/6 (+950.244µs): return nil
                                  Gather#985 ends at 1.132133ms
                              Combine#988 step 15/16 (+162.328µs): 25.353µs self time
                              Combine#988 step 16/16 (+187.681µs): return nil
                              Combine#988 ends at 197.68µs
                          Plan#35 step 9/12 (+0s): scatter:
                            Task#973: pool=2
                            Task#973 step 1/2 (+0s): 3.782µs self time
                            Task#973 step 2/2 (+3.782µs): return nil
                            Task#973 ends at 3.782µs
                              Combine#973: index=4 flush=<nil>
                              Combine#973 step 1/2 (+0s): 1.007µs self time
                              Combine#973 step 2/2 (+1.007µs): return nil
                              Combine#973 ends at 4.789µs
                          Plan#35 step 10/12 (+0s): scatter:
                            Task#976: pool=0
                            Task#976 step 1/2 (+0s): 10.005µs self time
                            Task#976 step 2/2 (+10.005µs): return error
                            Task#976 ends at 10.005µs
                              Gather#976: index=1
                              Gather#976 step 1/2 (+0s): 868ns self time
                              Gather#976 step 2/2 (+868ns): return nil
                              Gather#976 ends at 10.873µs
                          Plan#35 step 11/12 (+0s): scatter:
                            Task#960: pool=0
                            Task#960 step 1/2 (+0s): 10.002µs self time
                            Task#960 step 2/2 (+10.002µs): return nil
                            Task#960 ends at 10.002µs
                              Gather#960: index=1
                              Gather#960 step 1/2 (+0s): 998ns self time
                              Gather#960 step 2/2 (+998ns): return nil
                              Gather#960 ends at 11µs
                          Plan#35 step 12/12 (+0s): ends at 10.135826ms
                        Gather#955 step 3/6 (+10.136066ms): 430ns self time
                        Gather#955 step 4/6 (+10.136496ms): scatter:
                          Task#954: pool=9
                          Task#954 step 1/2 (+0s): 370.989µs self time
                          Task#954 step 2/2 (+370.989µs): return nil
                          Task#954 ends at 10.517481ms
                            Combine#954: index=0 flush=<nil>
                            Combine#954 step 1/10 (+0s): 743ns self time
                            Combine#954 step 2/10 (+743ns): scatter:
                              Task#951: pool=1
                              Task#951 step 1/2 (+0s): 9.997µs self time
                              Task#951 step 2/2 (+9.997µs): return nil
                              Task#951 ends at 10.528221ms
                                Gather#951: index=1
                                Gather#951 step 1/2 (+0s): 10.197µs self time
                                Gather#951 step 2/2 (+10.197µs): return nil
                                Gather#951 ends at 10.538418ms
                            Combine#954 step 3/10 (+743ns): 189ns self time
                            Combine#954 step 4/10 (+932ns): scatter:
                              Task#907: pool=7
                              Task#907 step 1/2 (+0s): 10.031µs self time
                              Task#907 step 2/2 (+10.031µs): return nil
                              Task#907 ends at 10.528444ms
                                Gather#907: index=6
                                Gather#907 step 1/2 (+0s): 931ns self time
                                Gather#907 step 2/2 (+931ns): return nil
                                Gather#907 ends at 10.529375ms
                            Combine#954 step 5/10 (+932ns): 19ns self time
                            Combine#954 step 6/10 (+951ns): scatter:
                              Task#906: pool=5
                              Task#906 step 1/2 (+0s): 12.142µs self time
                              Task#906 step 2/2 (+12.142µs): return nil
                              Task#906 ends at 10.530574ms
                                Gather#906: index=2
                                Gather#906 step 1/2 (+0s): 755ns self time
                                Gather#906 step 2/2 (+755ns): return error
                                Gather#906 ends at 10.531329ms
                            Combine#954 step 7/10 (+951ns): 26ns self time
                            Combine#954 step 8/10 (+977ns): scatter:
                              Task#953: pool=0
                              Task#953 step 1/2 (+0s): 9.998µs self time
                              Task#953 step 2/2 (+9.998µs): return nil
                              Task#953 ends at 10.528456ms
                                Gather#953: index=6
                                Gather#953 step 1/10 (+0s): 819ns self time
                                Gather#953 step 2/10 (+819ns): scatter:
                                  Task#952: pool=9
                                  Task#952 step 1/2 (+0s): 10.01µs self time
                                  Task#952 step 2/2 (+10.01µs): return nil
                                  Task#952 ends at 10.539285ms
                                    Gather#952: index=0
                                    Gather#952 step 1/10 (+0s): 219ns self time
                                    Gather#952 step 2/10 (+219ns): scatter:
                                      Task#947: pool=6
                                      Task#947 step 1/2 (+0s): 2.794095ms self time
                                      Task#947 step 2/2 (+2.794095ms): return nil
                                      Task#947 ends at 13.333599ms
                                        Gather#947: index=2
                                        Gather#947 step 1/2 (+0s): 1.032µs self time
                                        Gather#947 step 2/2 (+1.032µs): return nil
                                        Gather#947 ends at 13.334631ms
                                    Gather#952 step 3/10 (+219ns): 67ns self time
                                    Gather#952 step 4/10 (+286ns): scatter:
                                      Task#908: pool=5
                                      Task#908 step 1/2 (+0s): 9.993µs self time
                                      Task#908 step 2/2 (+9.993µs): return nil
                                      Task#908 ends at 10.549564ms
                                        Gather#908: index=2
                                        Gather#908 step 1/2 (+0s): 954ns self time
                                        Gather#908 step 2/2 (+954ns): return nil
                                        Gather#908 ends at 10.550518ms
                                    Gather#952 step 5/10 (+286ns): 230ns self time
                                    Gather#952 step 6/10 (+516ns): scatter:
                                      Task#903: pool=3
                                      Task#903 step 1/2 (+0s): 9.347µs self time
                                      Task#903 step 2/2 (+9.347µs): return nil
                                      Task#903 ends at 10.549148ms
                                        Combine#903: index=2 flush=<nil>
                                        Combine#903 step 1/2 (+0s): 1.054µs self time
                                        Combine#903 step 2/2 (+1.054µs): return error
                                        Combine#903 ends at 10.550202ms
                                    Gather#952 step 7/10 (+516ns): 241ns self time
                                    Gather#952 step 8/10 (+757ns): scatter:
                                      Task#905: pool=3
                                      Task#905 step 1/2 (+0s): 0s self time
                                      Task#905 step 2/2 (+0s): return nil
                                      Task#905 ends at 10.540042ms
                                        Combine#905: index=6 flush=<nil>
                                        Combine#905 step 1/2 (+0s): 57.044µs self time
                                        Combine#905 step 2/2 (+57.044µs): return nil
                                        Combine#905 ends at 10.597086ms
                                    Gather#952 step 9/10 (+757ns): 244ns self time
                                    Gather#952 step 10/10 (+1.001µs): return nil
                                    Gather#952 ends at 10.540286ms
                                Gather#953 step 3/10 (+819ns): 0s self time
                                Gather#953 step 4/10 (+819ns): scatter:
                                  Task#901: pool=6
                                  Task#901 step 1/2 (+0s): 9.997µs self time
                                  Task#901 step 2/2 (+9.997µs): return nil
                                  Task#901 ends at 10.539272ms
                                    Gather#901: index=6
                                    Gather#901 step 1/2 (+0s): 986ns self time
                                    Gather#901 step 2/2 (+986ns): return nil
                                    Gather#901 ends at 10.540258ms
                                Gather#953 step 5/10 (+819ns): 64ns self time
                                Gather#953 step 6/10 (+883ns): scatter:
                                  Task#909: pool=5
                                  Task#909 step 1/2 (+0s): 9.998µs self time
                                  Task#909 step 2/2 (+9.998µs): return nil
                                  Task#909 ends at 10.539337ms
                                    Gather#909: index=2
                                    Gather#909 step 1/2 (+0s): 143.727µs self time
                                    Gather#909 step 2/2 (+143.727µs): return nil
                                    Gather#909 ends at 10.683064ms
                                Gather#953 step 7/10 (+883ns): 21ns self time
                                Gather#953 step 8/10 (+904ns): scatter:
                                  Task#910: pool=0
                                  Task#910 step 1/2 (+0s): 10.011µs self time
                                  Task#910 step 2/2 (+10.011µs): return nil
                                  Task#910 ends at 10.539371ms
                                    Gather#910: index=3
                                    Gather#910 step 1/4 (+0s): 492ns self time
                                    Gather#910 step 2/4 (+492ns): subjob:
                                      Plan#34: pathCount=20 taskCount=36 maxPathDuration=8.611944ms minGatherCount=28 maxGatherCount=42
                                         TaskPools[0]: TaskPool#111: limit=1
                                         CombinerPools[0]: CombinerPool#155: limit=4
                                         CombinerPools[1]: CombinerPool#156: limit=1
                                         Combiners[0]: pool=1
                                         Combiners[1]: pool=1
                                         Combiners[2]: pool=0
                                         Combiners[3]: pool=1
                                         Combiners[4]: pool=0
                                         Combiners[5]: pool=1
                                         Combiners[6]: pool=0
                                         Combiners[7]: pool=0
                                         Combiners[8]: pool=1
                                         Combiners[9]: pool=1
                                         Combiners[10]: pool=1
                                         Combiners[11]: pool=0
                                         Combiners[12]: pool=1
                                         Combiners[13]: pool=1
                                         Combiners[14]: pool=1
                                         Combiners[15]: pool=0
                                         Combiners[16]: pool=1
                                         Combiners[17]: pool=0
                                         Combiners[18]: pool=1
                                         Combiners[19]: pool=1
                                      Plan#34 step 1/8 (+0s): scatter:
                                        Task#918: pool=0
                                        Task#918 step 1/2 (+0s): 8.610945ms self time
                                        Task#918 step 2/2 (+8.610945ms): return nil
                                        Task#918 ends at 8.610945ms
                                          Gather#918: index=1
                                          Gather#918 step 1/2 (+0s): 999ns self time
                                          Gather#918 step 2/2 (+999ns): return nil
                                          Gather#918 ends at 8.611944ms
                                      Plan#34 step 2/8 (+0s): scatter:
                                        Task#911: pool=0
                                        Task#911 step 1/2 (+0s): 10.201µs self time
                                        Task#911 step 2/2 (+10.201µs): return nil
                                        Task#911 ends at 10.201µs
                                          Combine#911: index=0 flush=<nil>
                                          Combine#911 step 1/2 (+0s): 1.001µs self time
                                          Combine#911 step 2/2 (+1.001µs): return nil
                                          Combine#911 ends at 11.202µs
                                      Plan#34 step 3/8 (+0s): scatter:
                                        Task#917: pool=0
                                        Task#917 step 1/2 (+0s): 9.836µs self time
                                        Task#917 step 2/2 (+9.836µs): return nil
                                        Task#917 ends at 9.836µs
                                          Combine#917: index=0 flush=<nil>
                                          Combine#917 step 1/2 (+0s): 1.014µs self time
                                          Combine#917 step 2/2 (+1.014µs): return nil
                                          Combine#917 ends at 10.85µs
                                      Plan#34 step 4/8 (+0s): scatter:
                                        Task#945: pool=0
                                        Task#945 step 1/2 (+0s): 9.999µs self time
                                        Task#945 step 2/2 (+9.999µs): return nil
                                        Task#945 ends at 9.999µs
                                          Gather#945: index=1
                                          Gather#945 step 1/4 (+0s): 61.147µs self time
                                          Gather#945 step 2/4 (+61.147µs): scatter:
                                            Task#913: pool=0
                                            Task#913 step 1/2 (+0s): 9.985µs self time
                                            Task#913 step 2/2 (+9.985µs): return nil
                                            Task#913 ends at 81.131µs
                                              Gather#913: index=0
                                              Gather#913 step 1/2 (+0s): 1.01µs self time
                                              Gather#913 step 2/2 (+1.01µs): return nil
                                              Gather#913 ends at 82.141µs
                                          Gather#945 step 3/4 (+61.147µs): 210.076µs self time
                                          Gather#945 step 4/4 (+271.223µs): return nil
                                          Gather#945 ends at 281.222µs
                                      Plan#34 step 5/8 (+0s): scatter:
                                        Task#921: pool=0
                                        Task#921 step 1/2 (+0s): 8.704µs self time
                                        Task#921 step 2/2 (+8.704µs): return nil
                                        Task#921 ends at 8.704µs
                                          Gather#921: index=2
                                          Gather#921 step 1/2 (+0s): 52.105µs self time
                                          Gather#921 step 2/2 (+52.105µs): return nil
                                          Gather#921 ends at 60.809µs
                                      Plan#34 step 6/8 (+0s): scatter:
                                        Task#944: pool=0
                                        Task#944 step 1/2 (+0s): 29.348µs self time
                                        Task#944 step 2/2 (+29.348µs): return nil
                                        Task#944 ends at 29.348µs
                                          Gather#944: index=2
                                          Gather#944 step 1/12 (+0s): 164ns self time
                                          Gather#944 step 2/12 (+164ns): scatter:
                                            Task#923: pool=0
                                            Task#923 step 1/2 (+0s): 9.986µs self time
                                            Task#923 step 2/2 (+9.986µs): return nil
                                            Task#923 ends at 39.498µs
                                              Gather#923: index=1
                                              Gather#923 step 1/2 (+0s): 1.005µs self time
                                              Gather#923 step 2/2 (+1.005µs): return nil
                                              Gather#923 ends at 40.503µs
                                          Gather#944 step 3/12 (+164ns): 161ns self time
                                          Gather#944 step 4/12 (+325ns): scatter:
                                            Task#930: pool=0
                                            Task#930 step 1/2 (+0s): 4.653µs self time
                                            Task#930 step 2/2 (+4.653µs): return nil
                                            Task#930 ends at 34.326µs
                                              Gather#930: index=0
                                              Gather#930 step 1/2 (+0s): 991ns self time
                                              Gather#930 step 2/2 (+991ns): return nil
                                              Gather#930 ends at 35.317µs
                                          Gather#944 step 5/12 (+325ns): 156ns self time
                                          Gather#944 step 6/12 (+481ns): scatter:
                                            Task#943: pool=0
                                            Task#943 step 1/2 (+0s): 0s self time
                                            Task#943 step 2/2 (+0s): return nil
                                            Task#943 ends at 29.829µs
                                              Gather#943: index=2
                                              Gather#943 step 1/4 (+0s): 474ns self time
                                              Gather#943 step 2/4 (+474ns): scatter:
                                                Task#938: pool=0
                                                Task#938 step 1/2 (+0s): 9.752µs self time
                                                Task#938 step 2/2 (+9.752µs): return nil
                                                Task#938 ends at 40.055µs
                                                  Combine#938: index=1 flush=<nil>
                                                  Combine#938 step 1/16 (+0s): 957ns self time
                                                  Combine#938 step 2/16 (+957ns): scatter:
                                                    Task#919: pool=0
                                                    Task#919 step 1/2 (+0s): 9.999µs self time
                                                    Task#919 step 2/2 (+9.999µs): return nil
                                                    Task#919 ends at 51.011µs
                                                      Gather#919: index=0
                                                      Gather#919 step 1/2 (+0s): 1.012µs self time
                                                      Gather#919 step 2/2 (+1.012µs): return nil
                                                      Gather#919 ends at 52.023µs
                                                  Combine#938 step 3/16 (+957ns): 0s self time
                                                  Combine#938 step 4/16 (+957ns): scatter:
                                                    Task#934: pool=0
                                                    Task#934 step 1/2 (+0s): 9.489µs self time
                                                    Task#934 step 2/2 (+9.489µs): return nil
                                                    Task#934 ends at 50.501µs
                                                      Gather#934: index=1
                                                      Gather#934 step 1/6 (+0s): 161ns self time
                                                      Gather#934 step 2/6 (+161ns): scatter:
                                                        Task#915: pool=0
                                                        Task#915 step 1/2 (+0s): 4.881µs self time
                                                        Task#915 step 2/2 (+4.881µs): return nil
                                                        Task#915 ends at 55.543µs
                                                          Gather#915: index=0
                                                          Gather#915 step 1/2 (+0s): 1.062µs self time
                                                          Gather#915 step 2/2 (+1.062µs): return nil
                                                          Gather#915 ends at 56.605µs
                                                      Gather#934 step 3/6 (+161ns): 55ns self time
                                                      Gather#934 step 4/6 (+216ns): scatter:
                                                        Task#927: pool=0
                                                        Task#927 step 1/2 (+0s): 6.636µs self time
                                                        Task#927 step 2/2 (+6.636µs): return nil
                                                        Task#927 ends at 57.353µs
                                                          Gather#927: index=2
                                                          Gather#927 step 1/2 (+0s): 982ns self time
                                                          Gather#927 step 2/2 (+982ns): return nil
                                                          Gather#927 ends at 58.335µs
                                                      Gather#934 step 5/6 (+216ns): 275ns self time
                                                      Gather#934 step 6/6 (+491ns): return nil
                                                      Gather#934 ends at 50.992µs
                                                  Combine#938 step 5/16 (+957ns): 2ns self time
                                                  Combine#938 step 6/16 (+959ns): scatter:
                                                    Task#924: pool=0
                                                    Task#924 step 1/2 (+0s): 9.994µs self time
                                                    Task#924 step 2/2 (+9.994µs): return nil
                                                    Task#924 ends at 51.008µs
                                                      Gather#924: index=1
                                                      Gather#924 step 1/2 (+0s): 1.001µs self time
                                                      Gather#924 step 2/2 (+1.001µs): return nil
                                                      Gather#924 ends at 52.009µs
                                                  Combine#938 step 7/16 (+959ns): 34ns self time
                                                  Combine#938 step 8/16 (+993ns): scatter:
                                                    Task#928: pool=0
                                                    Task#928 step 1/2 (+0s): 190.436µs self time
                                                    Task#928 step 2/2 (+190.436µs): return nil
                                                    Task#928 ends at 231.484µs
                                                      Gather#928: index=0
                                                      Gather#928 step 1/2 (+0s): 999ns self time
                                                      Gather#928 step 2/2 (+999ns): return error
                                                      Gather#928 ends at 232.483µs
                                                  Combine#938 step 9/16 (+993ns): 4ns self time
                                                  Combine#938 step 10/16 (+997ns): scatter:
                                                    Task#912: pool=0
                                                    Task#912 step 1/2 (+0s): 10µs self time
                                                    Task#912 step 2/2 (+10µs): return nil
                                                    Task#912 ends at 51.052µs
                                                      Gather#912: index=0
                                                      Gather#912 step 1/2 (+0s): 328.754µs self time
                                                      Gather#912 step 2/2 (+328.754µs): return nil
                                                      Gather#912 ends at 379.806µs
                                                  Combine#938 step 11/16 (+997ns): 4ns self time
                                                  Combine#938 step 12/16 (+1.001µs): scatter:
                                                    Task#926: pool=0
                                                    Task#926 step 1/2 (+0s): 9.853µs self time
                                                    Task#926 step 2/2 (+9.853µs): return error
                                                    Task#926 ends at 50.909µs
                                                      Gather#926: index=1
                                                      Gather#926 step 1/2 (+0s): 320ns self time
                                                      Gather#926 step 2/2 (+320ns): return nil
                                                      Gather#926 ends at 51.229µs
                                                  Combine#938 step 13/16 (+1.001µs): 2ns self time
                                                  Combine#938 step 14/16 (+1.003µs): scatter:
                                                    Task#933: pool=0
                                                    Task#933 step 1/2 (+0s): 10.001µs self time
                                                    Task#933 step 2/2 (+10.001µs): return nil
                                                    Task#933 ends at 51.059µs
                                                      Combine#933: index=0 flush=<nil>
                                                      Combine#933 step 1/4 (+0s): 420.554µs self time
                                                      Combine#933 step 2/4 (+420.554µs): scatter:
                                                        Task#922: pool=0
                                                        Task#922 step 1/2 (+0s): 10.007µs self time
                                                        Task#922 step 2/2 (+10.007µs): return nil
                                                        Task#922 ends at 481.62µs
                                                          Combine#922: index=1 flush=<nil>
                                                          Combine#922 step 1/2 (+0s): 998ns self time
                                                          Combine#922 step 2/2 (+998ns): return nil
                                                          Combine#922 ends at 482.618µs
                                                      Combine#933 step 3/4 (+420.554µs): 423.49µs self time
                                                      Combine#933 step 4/4 (+844.044µs): return error
                                                      Combine#933 ends at 895.103µs
                                                  Combine#938 step 15/16 (+1.003µs): 0s self time
                                                  Combine#938 step 16/16 (+1.003µs): return nil
                                                  Combine#938 ends at 41.058µs
                                              Gather#943 step 3/4 (+474ns): 483ns self time
                                              Gather#943 step 4/4 (+957ns): return nil
                                              Gather#943 ends at 30.786µs
                                          Gather#944 step 7/12 (+481ns): 179ns self time
                                          Gather#944 step 8/12 (+660ns): scatter:
                                            Task#940: pool=0
                                            Task#940 step 1/2 (+0s): 10.007µs self time
                                            Task#940 step 2/2 (+10.007µs): return nil
                                            Task#940 ends at 40.015µs
                                              Gather#940: index=1
                                              Gather#940 step 1/4 (+0s): 1ms self time
                                              Gather#940 step 2/4 (+1ms): scatter:
                                                Task#936: pool=0
                                                Task#936 step 1/2 (+0s): 5.346µs self time
                                                Task#936 step 2/2 (+5.346µs): return nil
                                                Task#936 ends at 1.045361ms
                                                  Gather#936: index=1
                                                  Gather#936 step 1/4 (+0s): 2.42µs self time
                                                  Gather#936 step 2/4 (+2.42µs): scatter:
                                                    Task#935: pool=0
                                                    Task#935 step 1/2 (+0s): 1.108µs self time
                                                    Task#935 step 2/2 (+1.108µs): return error
                                                    Task#935 ends at 1.048889ms
                                                      Gather#935: index=0
                                                      Gather#935 step 1/4 (+0s): 37.218µs self time
                                                      Gather#935 step 2/4 (+37.218µs): scatter:
                                                        Task#925: pool=0
                                                        Task#925 step 1/2 (+0s): 10µs self time
                                                        Task#925 step 2/2 (+10µs): return nil
                                                        Task#925 ends at 1.096107ms
                                                          Gather#925: index=2
                                                          Gather#925 step 1/2 (+0s): 33.621µs self time
                                                          Gather#925 step 2/2 (+33.621µs): return nil
                                                          Gather#925 ends at 1.129728ms
                                                      Gather#935 step 3/4 (+37.218µs): 37.19µs self time
                                                      Gather#935 step 4/4 (+74.408µs): return nil
                                                      Gather#935 ends at 1.123297ms
                                                  Gather#936 step 3/4 (+2.42µs): 2.425µs self time
                                                  Gather#936 step 4/4 (+4.845µs): return nil
                                                  Gather#936 ends at 1.050206ms
                                              Gather#940 step 3/4 (+1ms): 0s self time
                                              Gather#940 step 4/4 (+1ms): return nil
                                              Gather#940 ends at 1.040015ms
                                          Gather#944 step 9/12 (+660ns): 255ns self time
                                          Gather#944 step 10/12 (+915ns): scatter:
                                            Task#942: pool=0
                                            Task#942 step 1/2 (+0s): 8.865µs self time
                                            Task#942 step 2/2 (+8.865µs): return error
                                            Task#942 ends at 39.128µs
                                              Gather#942: index=1
                                              Gather#942 step 1/4 (+0s): 17ns self time
                                              Gather#942 step 2/4 (+17ns): scatter:
                                                Task#939: pool=0
                                                Task#939 step 1/2 (+0s): 12.776µs self time
                                                Task#939 step 2/2 (+12.776µs): return nil
                                                Task#939 ends at 51.921µs
                                                  Gather#939: index=2
                                                  Gather#939 step 1/6 (+0s): 274ns self time
                                                  Gather#939 step 2/6 (+274ns): scatter:
                                                    Task#914: pool=0
                                                    Task#914 step 1/2 (+0s): 9.998µs self time
                                                    Task#914 step 2/2 (+9.998µs): return nil
                                                    Task#914 ends at 62.193µs
                                                      Gather#914: index=0
                                                      Gather#914 step 1/2 (+0s): 1.02µs self time
                                                      Gather#914 step 2/2 (+1.02µs): return nil
                                                      Gather#914 ends at 63.213µs
                                                  Gather#939 step 3/6 (+274ns): 193ns self time
                                                  Gather#939 step 4/6 (+467ns): scatter:
                                                    Task#931: pool=0
                                                    Task#931 step 1/2 (+0s): 9.943µs self time
                                                    Task#931 step 2/2 (+9.943µs): return nil
                                                    Task#931 ends at 62.331µs
                                                      Gather#931: index=1
                                                      Gather#931 step 1/4 (+0s): 556ns self time
                                                      Gather#931 step 2/4 (+556ns): scatter:
                                                        Task#916: pool=0
                                                        Task#916 step 1/2 (+0s): 9.53µs self time
                                                        Task#916 step 2/2 (+9.53µs): return nil
                                                        Task#916 ends at 72.417µs
                                                          Gather#916: index=2
                                                          Gather#916 step 1/2 (+0s): 1.001µs self time
                                                          Gather#916 step 2/2 (+1.001µs): return nil
                                                          Gather#916 ends at 73.418µs
                                                      Gather#931 step 3/4 (+556ns): 556ns self time
                                                      Gather#931 step 4/4 (+1.112µs): return nil
                                                      Gather#931 ends at 63.443µs
                                                  Gather#939 step 5/6 (+467ns): 532ns self time
                                                  Gather#939 step 6/6 (+999ns): return nil
                                                  Gather#939 ends at 52.92µs
                                              Gather#942 step 3/4 (+17ns): 19ns self time
                                              Gather#942 step 4/4 (+36ns): return nil
                                              Gather#942 ends at 39.164µs
                                          Gather#944 step 11/12 (+915ns): 87ns self time
                                          Gather#944 step 12/12 (+1.002µs): return error
                                          Gather#944 ends at 30.35µs
                                      Plan#34 step 7/8 (+0s): scatter:
                                        Task#946: pool=0
                                        Task#946 step 1/2 (+0s): 10.005µs self time
                                        Task#946 step 2/2 (+10.005µs): return nil
                                        Task#946 ends at 10.005µs
                                          Gather#946: index=1
                                          Gather#946 step 1/4 (+0s): 777ns self time
                                          Gather#946 step 2/4 (+777ns): scatter:
                                            Task#941: pool=0
                                            Task#941 step 1/2 (+0s): 10.393µs self time
                                            Task#941 step 2/2 (+10.393µs): return nil
                                            Task#941 ends at 21.175µs
                                              Gather#941: index=2
                                              Gather#941 step 1/6 (+0s): 207ns self time
                                              Gather#941 step 2/6 (+207ns): scatter:
                                                Task#920: pool=0
                                                Task#920 step 1/2 (+0s): 9.995µs self time
                                                Task#920 step 2/2 (+9.995µs): return nil
                                                Task#920 ends at 31.377µs
                                                  Gather#920: index=2
                                                  Gather#920 step 1/2 (+0s): 2.675µs self time
                                                  Gather#920 step 2/2 (+2.675µs): return nil
                                                  Gather#920 ends at 34.052µs
                                              Gather#941 step 3/6 (+207ns): 213ns self time
                                              Gather#941 step 4/6 (+420ns): scatter:
                                                Task#937: pool=0
                                                Task#937 step 1/2 (+0s): 9.996µs self time
                                                Task#937 step 2/2 (+9.996µs): return nil
                                                Task#937 ends at 31.591µs
                                                  Combine#937: index=0 flush=<nil>
                                                  Combine#937 step 1/4 (+0s): 72ns self time
                                                  Combine#937 step 2/4 (+72ns): scatter:
                                                    Task#932: pool=0
                                                    Task#932 step 1/2 (+0s): 9.998µs self time
                                                    Task#932 step 2/2 (+9.998µs): return nil
                                                    Task#932 ends at 41.661µs
                                                      Combine#932: index=15 flush=<nil>
                                                      Combine#932 step 1/4 (+0s): 0s self time
                                                      Combine#932 step 2/4 (+0s): scatter:
                                                        Task#929: pool=0
                                                        Task#929 step 1/2 (+0s): 9.997µs self time
                                                        Task#929 step 2/2 (+9.997µs): return nil
                                                        Task#929 ends at 51.658µs
                                                          Combine#929: index=6 flush=<nil>
                                                          Combine#929 step 1/2 (+0s): 6.922µs self time
                                                          Combine#929 step 2/2 (+6.922µs): return nil
                                                          Combine#929 ends at 58.58µs
                                                      Combine#932 step 3/4 (+0s): 1.92µs self time
                                                      Combine#932 step 4/4 (+1.92µs): return nil
                                                      Combine#932 ends at 43.581µs
                                                  Combine#937 step 3/4 (+72ns): 61ns self time
                                                  Combine#937 step 4/4 (+133ns): return error
                                                  Combine#937 ends at 31.724µs
                                              Gather#941 step 5/6 (+420ns): 221ns self time
                                              Gather#941 step 6/6 (+641ns): return nil
                                              Gather#941 ends at 21.816µs
                                          Gather#946 step 3/4 (+777ns): 60ns self time
                                          Gather#946 step 4/4 (+837ns): return nil
                                          Gather#946 ends at 10.842µs
                                      Plan#34 step 8/8 (+0s): ends at 8.611944ms
                                    Gather#910 step 3/4 (+8.612436ms): 516ns self time
                                    Gather#910 step 4/4 (+8.612952ms): return nil
                                    Gather#910 ends at 19.152323ms
                                Gather#953 step 9/10 (+904ns): 95ns self time
                                Gather#953 step 10/10 (+999ns): return nil
                                Gather#953 ends at 10.529455ms
                            Combine#954 step 9/10 (+977ns): 24ns self time
                            Combine#954 step 10/10 (+1.001µs): return nil
                            Combine#954 ends at 10.518482ms
                        Gather#955 step 5/6 (+10.136496ms): 453ns self time
                        Gather#955 step 6/6 (+10.136949ms): return error
                        Gather#955 ends at 10.146945ms
                    Plan#33 step 5/7 (+0s): scatter:
                      Task#989: pool=2
                      Task#989 step 1/2 (+0s): 9.992µs self time
                      Task#989 step 2/2 (+9.992µs): return error
                      Task#989 ends at 9.992µs
                        Combine#989: index=1 flush=<nil>
                        Combine#989 step 1/4 (+0s): 168ns self time
                        Combine#989 step 2/4 (+168ns): scatter:
                          Task#950: pool=6
                          Task#950 step 1/2 (+0s): 10.02µs self time
                          Task#950 step 2/2 (+10.02µs): return nil
                          Task#950 ends at 20.18µs
                            Gather#950: index=5
                            Gather#950 step 1/2 (+0s): 1µs self time
                            Gather#950 step 2/2 (+1µs): return nil
                            Gather#950 ends at 21.18µs
                        Combine#989 step 3/4 (+168ns): 3ns self time
                        Combine#989 step 4/4 (+171ns): return nil
                        Combine#989 ends at 10.163µs
                    Plan#33 step 6/7 (+0s): scatter:
                      Task#992: pool=2
                      Task#992 step 1/2 (+0s): 5.10464ms self time
                      Task#992 step 2/2 (+5.10464ms): return nil
                      Task#992 ends at 5.10464ms
                        Gather#992: index=3
                        Gather#992 step 1/4 (+0s): 1.561µs self time
                        Gather#992 step 2/4 (+1.561µs): scatter:
                          Task#902: pool=4
                          Task#902 step 1/2 (+0s): 5.604µs self time
                          Task#902 step 2/2 (+5.604µs): return nil
                          Task#902 ends at 5.111805ms
                            Gather#902: index=5
                            Gather#902 step 1/2 (+0s): 666ns self time
                            Gather#902 step 2/2 (+666ns): return nil
                            Gather#902 ends at 5.112471ms
                        Gather#992 step 3/4 (+1.561µs): 704ns self time
                        Gather#992 step 4/4 (+2.265µs): return nil
                        Gather#992 ends at 5.106905ms
                    Plan#33 step 7/7 (+0s): ends at 19.152323ms
                  Gather#900 step 7/10 (+19.152814ms): 160ns self time
                  Gather#900 step 8/10 (+19.152974ms): scatter:
                    Task#701: pool=0
                    Task#701 step 1/2 (+0s): 9.999µs self time
                    Task#701 step 2/2 (+9.999µs): return nil
                    Task#701 ends at 34.989286ms
                      Gather#701: index=2
                      Gather#701 step 1/4 (+0s): 508ns self time
                      Gather#701 step 2/4 (+508ns): scatter:
                        Task#700: pool=0
                        Task#700 step 1/2 (+0s): 10µs self time
                        Task#700 step 2/2 (+10µs): return nil
                        Task#700 ends at 34.999794ms
                          Gather#700: index=3
                          Gather#700 step 1/8 (+0s): 1.83µs self time
                          Gather#700 step 2/8 (+1.83µs): scatter:
                            Task#544: pool=0
                            Task#544 step 1/2 (+0s): 8.9µs self time
                            Task#544 step 2/2 (+8.9µs): return nil
                            Task#544 ends at 35.010524ms
                              Gather#544: index=1
                              Gather#544 step 1/4 (+0s): 510ns self time
                              Gather#544 step 2/4 (+510ns): scatter:
                                Task#286: pool=0
                                Task#286 step 1/2 (+0s): 10.023µs self time
                                Task#286 step 2/2 (+10.023µs): return nil
                                Task#286 ends at 35.021057ms
                                  Gather#286: index=0
                                  Gather#286 step 1/2 (+0s): 897ns self time
                                  Gather#286 step 2/2 (+897ns): return nil
                                  Gather#286 ends at 35.021954ms
                              Gather#544 step 3/4 (+510ns): 491ns self time
                              Gather#544 step 4/4 (+1.001µs): return nil
                              Gather#544 ends at 35.011525ms
                          Gather#700 step 3/8 (+1.83µs): 8ns self time
                          Gather#700 step 4/8 (+1.838µs): scatter:
                            Task#283: pool=0
                            Task#283 step 1/2 (+0s): 9.988µs self time
                            Task#283 step 2/2 (+9.988µs): return nil
                            Task#283 ends at 35.01162ms
                              Combine#283: index=1 flush=<nil>
                              Combine#283 step 1/2 (+0s): 996ns self time
                              Combine#283 step 2/2 (+996ns): return error
                              Combine#283 ends at 35.012616ms
                          Gather#700 step 5/8 (+1.838µs): 13ns self time
                          Gather#700 step 6/8 (+1.851µs): scatter:
                            Task#285: pool=0
                            Task#285 step 1/2 (+0s): 7.059µs self time
                            Task#285 step 2/2 (+7.059µs): return nil
                            Task#285 ends at 35.008704ms
                              Gather#285: index=2
                              Gather#285 step 1/2 (+0s): 149.834µs self time
                              Gather#285 step 2/2 (+149.834µs): return nil
                              Gather#285 ends at 35.158538ms
                          Gather#700 step 7/8 (+1.851µs): 14ns self time
                          Gather#700 step 8/8 (+1.865µs): return nil
                          Gather#700 ends at 35.001659ms
                      Gather#701 step 3/4 (+508ns): 491ns self time
                      Gather#701 step 4/4 (+999ns): return nil
                      Gather#701 ends at 34.990285ms
                  Gather#900 step 9/10 (+19.152974ms): 183ns self time
                  Gather#900 step 10/10 (+19.153157ms): return nil
                  Gather#900 ends at 34.97947ms
              Plan#12 step 10/10 (+0s): ends at 35.158538ms
            Combine#268 step 5/6 (+35.159179ms): 328ns self time
            Combine#268 step 6/6 (+35.159507ms): return nil
            Combine#268 ends at 35.186905ms
        Gather#1056 step 3/12 (+283ns): 16ns self time
        Gather#1056 step 4/12 (+299ns): scatter:
          Task#126: pool=0
          Task#126 step 1/2 (+0s): 9.811µs self time
          Task#126 step 2/2 (+9.811µs): return nil
          Task#126 ends at 27.227µs
            Combine#126: index=5 flush=<nil>
            Combine#126 step 1/2 (+0s): 1.043µs self time
            Combine#126 step 2/2 (+1.043µs): return nil
            Combine#126 ends at 28.27µs
        Gather#1056 step 5/12 (+299ns): 185ns self time
        Gather#1056 step 6/12 (+484ns): scatter:
          Task#128: pool=1
          Task#128 step 1/2 (+0s): 9.98µs self time
          Task#128 step 2/2 (+9.98µs): return nil
          Task#128 ends at 27.581µs
            Combine#128: index=14 flush=Gather#128
            Combine#128 step 1/2 (+0s): 1.057µs self time
            Combine#128 step 2/2 (+1.057µs): return nil
            Combine#128 ends at 28.638µs
              Gather#128: index=5
              Gather#128 step 1/2 (+0s): 894.471µs self time
              Gather#128 step 2/2 (+894.471µs): return nil
              Gather#128 ends at 0s
        Gather#1056 step 7/12 (+484ns): 145ns self time
        Gather#1056 step 8/12 (+629ns): scatter:
          Task#1055: pool=1
          Task#1055 step 1/2 (+0s): 9.213µs self time
          Task#1055 step 2/2 (+9.213µs): return nil
          Task#1055 ends at 26.959µs
            Gather#1055: index=2
            Gather#1055 step 1/6 (+0s): 198ns self time
            Gather#1055 step 2/6 (+198ns): scatter:
              Task#267: pool=1
              Task#267 step 1/2 (+0s): 9.843µs self time
              Task#267 step 2/2 (+9.843µs): return nil
              Task#267 ends at 37µs
                Combine#267: index=3 flush=<nil>
                Combine#267 step 1/4 (+0s): 731ns self time
                Combine#267 step 2/4 (+731ns): scatter:
                  Task#266: pool=0
                  Task#266 step 1/2 (+0s): 10.015µs self time
                  Task#266 step 2/2 (+10.015µs): return nil
                  Task#266 ends at 47.746µs
                    Gather#266: index=4
                    Gather#266 step 1/2 (+0s): 1.023µs self time
                    Gather#266 step 2/2 (+1.023µs): return nil
                    Gather#266 ends at 48.769µs
                Combine#267 step 3/4 (+731ns): 268ns self time
                Combine#267 step 4/4 (+999ns): return nil
                Combine#267 ends at 37.999µs
            Gather#1055 step 3/6 (+198ns): 0s self time
            Gather#1055 step 4/6 (+198ns): scatter:
              Task#0: pool=0
              Task#0 step 1/2 (+0s): 10.209µs self time
              Task#0 step 2/2 (+10.209µs): return nil
              Task#0 ends at 37.366µs
                Gather#0: index=3
                Gather#0 step 1/4 (+0s): 505ns self time
                Gather#0 step 2/4 (+505ns): subjob:
                  Plan#1: pathCount=15 taskCount=28 maxPathDuration=13.758854ms minGatherCount=19 maxGatherCount=33
                     TaskPools[0]: TaskPool#2: limit=4
                     TaskPools[1]: TaskPool#3: limit=6
                     TaskPools[2]: TaskPool#4: limit=10
                     TaskPools[3]: TaskPool#5: limit=2
                     TaskPools[4]: TaskPool#6: limit=10
                     TaskPools[5]: TaskPool#7: limit=1
                     TaskPools[6]: TaskPool#8: limit=6
                     TaskPools[7]: TaskPool#9: limit=5
                     TaskPools[8]: TaskPool#10: limit=7
                     TaskPools[9]: TaskPool#11: limit=4
                     CombinerPools[0]: CombinerPool#1: limit=2
                     CombinerPools[1]: CombinerPool#2: limit=1
                     Combiners[0]: pool=1
                     Combiners[1]: pool=0
                     Combiners[2]: pool=1
                     Combiners[3]: pool=1
                  Plan#1 step 1/6 (+0s): scatter:
                    Task#123: pool=1
                    Task#123 step 1/2 (+0s): 9.998µs self time
                    Task#123 step 2/2 (+9.998µs): return nil
                    Task#123 ends at 9.998µs
                      Gather#123: index=2
                      Gather#123 step 1/4 (+0s): 490ns self time
                      Gather#123 step 2/4 (+490ns): scatter:
                        Task#121: pool=6
                        Task#121 step 1/2 (+0s): 10.454µs self time
                        Task#121 step 2/2 (+10.454µs): return nil
                        Task#121 ends at 20.942µs
                          Combine#121: index=1 flush=<nil>
                          Combine#121 step 1/4 (+0s): 4ns self time
                          Combine#121 step 2/4 (+4ns): scatter:
                            Task#5: pool=3
                            Task#5 step 1/2 (+0s): 0s self time
                            Task#5 step 2/2 (+0s): return nil
                            Task#5 ends at 20.946µs
                              Gather#5: index=0
                              Gather#5 step 1/2 (+0s): 1µs self time
                              Gather#5 step 2/2 (+1µs): return nil
                              Gather#5 ends at 21.946µs
                          Combine#121 step 3/4 (+4ns): 1.009µs self time
                          Combine#121 step 4/4 (+1.013µs): return nil
                          Combine#121 ends at 21.955µs
                      Gather#123 step 3/4 (+490ns): 489ns self time
                      Gather#123 step 4/4 (+979ns): return nil
                      Gather#123 ends at 10.977µs
                  Plan#1 step 2/6 (+0s): scatter:
                    Task#122: pool=7
                    Task#122 step 1/2 (+0s): 9.951µs self time
                    Task#122 step 2/2 (+9.951µs): return error
                    Task#122 ends at 9.951µs
                      Gather#122: index=4
                      Gather#122 step 1/8 (+0s): 15.483µs self time
                      Gather#122 step 2/8 (+15.483µs): scatter:
                        Task#118: pool=1
                        Task#118 step 1/2 (+0s): 10.153µs self time
                        Task#118 step 2/2 (+10.153µs): return error
                        Task#118 ends at 35.587µs
                          Gather#118: index=4
                          Gather#118 step 1/4 (+0s): 0s self time
                          Gather#118 step 2/4 (+0s): scatter:
                            Task#2: pool=9
                            Task#2 step 1/2 (+0s): 10.014µs self time
                            Task#2 step 2/2 (+10.014µs): return nil
                            Task#2 ends at 45.601µs
                              Combine#2: index=1 flush=<nil>
                              Combine#2 step 1/2 (+0s): 438ns self time
                              Combine#2 step 2/2 (+438ns): return nil
                              Combine#2 ends at 46.039µs
                          Gather#118 step 3/4 (+0s): 0s self time
                          Gather#118 step 4/4 (+0s): return nil
                          Gather#118 ends at 35.587µs
                      Gather#122 step 3/8 (+15.483µs): 13.681µs self time
                      Gather#122 step 4/8 (+29.164µs): scatter:
                        Task#119: pool=7
                        Task#119 step 1/2 (+0s): 4.062µs self time
                        Task#119 step 2/2 (+4.062µs): return nil
                        Task#119 ends at 43.177µs
                          Combine#119: index=1 flush=<nil>
                          Combine#119 step 1/4 (+0s): 422ns self time
                          Combine#119 step 2/4 (+422ns): scatter:
                            Task#117: pool=4
                            Task#117 step 1/2 (+0s): 1.344µs self time
                            Task#117 step 2/2 (+1.344µs): return nil
                            Task#117 ends at 44.943µs
                              Combine#117: index=1 flush=<nil>
                              Combine#117 step 1/4 (+0s): 510ns self time
                              Combine#117 step 2/4 (+510ns): scatter:
                                Task#115: pool=0
                                Task#115 step 1/2 (+0s): 1.371µs self time
                                Task#115 step 2/2 (+1.371µs): return nil
                                Task#115 ends at 46.824µs
                                  Gather#115: index=0
                                  Gather#115 step 1/4 (+0s): 73ns self time
                                  Gather#115 step 2/4 (+73ns): scatter:
                                    Task#68: pool=1
                                    Task#68 step 1/2 (+0s): 10.255µs self time
                                    Task#68 step 2/2 (+10.255µs): return nil
                                    Task#68 ends at 57.152µs
                                      Gather#68: index=4
                                      Gather#68 step 1/2 (+0s): 1.097µs self time
                                      Gather#68 step 2/2 (+1.097µs): return nil
                                      Gather#68 ends at 58.249µs
                                  Gather#115 step 3/4 (+73ns): 933ns self time
                                  Gather#115 step 4/4 (+1.006µs): return nil
                                  Gather#115 ends at 47.83µs
                              Combine#117 step 3/4 (+510ns): 504ns self time
                              Combine#117 step 4/4 (+1.014µs): return nil
                              Combine#117 ends at 45.957µs
                          Combine#119 step 3/4 (+422ns): 412ns self time
                          Combine#119 step 4/4 (+834ns): return nil
                          Combine#119 ends at 44.011µs
                      Gather#122 step 5/8 (+29.164µs): 16.366µs self time
                      Gather#122 step 6/8 (+45.53µs): scatter:
                        Task#120: pool=5
                        Task#120 step 1/2 (+0s): 8.359µs self time
                        Task#120 step 2/2 (+8.359µs): return nil
                        Task#120 ends at 63.84µs
                          Combine#120: index=3 flush=<nil>
                          Combine#120 step 1/10 (+0s): 1.226µs self time
                          Combine#120 step 2/10 (+1.226µs): scatter:
                            Task#66: pool=4
                            Task#66 step 1/2 (+0s): 13.569µs self time
                            Task#66 step 2/2 (+13.569µs): return nil
                            Task#66 ends at 78.635µs
                              Combine#66: index=0 flush=<nil>
                              Combine#66 step 1/2 (+0s): 996ns self time
                              Combine#66 step 2/2 (+996ns): return nil
                              Combine#66 ends at 79.631µs
                          Combine#120 step 3/10 (+1.226µs): 1.225µs self time
                          Combine#120 step 4/10 (+2.451µs): scatter:
                            Task#116: pool=1
                            Task#116 step 1/2 (+0s): 9.974µs self time
                            Task#116 step 2/2 (+9.974µs): return nil
                            Task#116 ends at 76.265µs
                              Gather#116: index=0
                              Gather#116 step 1/6 (+0s): 24.862µs self time
                              Gather#116 step 2/6 (+24.862µs): scatter:
                                Task#70: pool=7
                                Task#70 step 1/2 (+0s): 9.204µs self time
                                Task#70 step 2/2 (+9.204µs): return nil
                                Task#70 ends at 110.331µs
                                  Gather#70: index=0
                                  Gather#70 step 1/8 (+0s): 87ns self time
                                  Gather#70 step 2/8 (+87ns): scatter:
                                    Task#63: pool=3
                                    Task#63 step 1/2 (+0s): 10.007µs self time
                                    Task#63 step 2/2 (+10.007µs): return nil
                                    Task#63 ends at 120.425µs
                                      Gather#63: index=2
                                      Gather#63 step 1/2 (+0s): 353.594µs self time
                                      Gather#63 step 2/2 (+353.594µs): return nil
                                      Gather#63 ends at 474.019µs
                                  Gather#70 step 3/8 (+87ns): 652ns self time
                                  Gather#70 step 4/8 (+739ns): scatter:
                                    Task#1: pool=9
                                    Task#1 step 1/2 (+0s): 167.697µs self time
                                    Task#1 step 2/2 (+167.697µs): return nil
                                    Task#1 ends at 278.767µs
                                      Gather#1: index=3
                                      Gather#1 step 1/2 (+0s): 809ns self time
                                      Gather#1 step 2/2 (+809ns): return nil
                                      Gather#1 ends at 279.576µs
                                  Gather#70 step 5/8 (+739ns): 99ns self time
                                  Gather#70 step 6/8 (+838ns): scatter:
                                    Task#4: pool=4
                                    Task#4 step 1/2 (+0s): 12.339µs self time
                                    Task#4 step 2/2 (+12.339µs): return nil
                                    Task#4 ends at 123.508µs
                                      Gather#4: index=2
                                      Gather#4 step 1/2 (+0s): 953ns self time
                                      Gather#4 step 2/2 (+953ns): return nil
                                      Gather#4 ends at 124.461µs
                                  Gather#70 step 7/8 (+838ns): 99ns self time
                                  Gather#70 step 8/8 (+937ns): return nil
                                  Gather#70 ends at 111.268µs
                              Gather#116 step 3/6 (+24.862µs): 28.563µs self time
                              Gather#116 step 4/6 (+53.425µs): scatter:
                                Task#71: pool=8
                                Task#71 step 1/4 (+0s): 3.177µs self time
                                Task#71 step 2/4 (+3.177µs): subjob:
                                  Plan#4: pathCount=8 taskCount=14 maxPathDuration=9.239344ms minGatherCount=12 maxGatherCount=15
                                     TaskPools[0]: TaskPool#21: limit=2
                                     TaskPools[1]: TaskPool#22: limit=2
                                     CombinerPools[0]: CombinerPool#12: limit=2
                                     CombinerPools[1]: CombinerPool#13: limit=1
                                     Combiners[0]: pool=1
                                     Combiners[1]: pool=1
                                     Combiners[2]: pool=0
                                     Combiners[3]: pool=1
                                     Combiners[4]: pool=1
                                  Plan#4 step 1/5 (+0s): scatter:
                                    Task#113: pool=0
                                    Task#113 step 1/2 (+0s): 10.082µs self time
                                    Task#113 step 2/2 (+10.082µs): return nil
                                    Task#113 ends at 10.082µs
                                      Gather#113: index=3
                                      Gather#113 step 1/4 (+0s): 546ns self time
                                      Gather#113 step 2/4 (+546ns): scatter:
                                        Task#112: pool=1
                                        Task#112 step 1/2 (+0s): 9.998µs self time
                                        Task#112 step 2/2 (+9.998µs): return nil
                                        Task#112 ends at 20.626µs
                                          Combine#112: index=3 flush=<nil>
                                          Combine#112 step 1/4 (+0s): 517ns self time
                                          Combine#112 step 2/4 (+517ns): scatter:
                                            Task#73: pool=1
                                            Task#73 step 1/2 (+0s): 948.337µs self time
                                            Task#73 step 2/2 (+948.337µs): return nil
                                            Task#73 ends at 969.48µs
                                              Gather#73: index=14
                                              Gather#73 step 1/2 (+0s): 1.002µs self time
                                              Gather#73 step 2/2 (+1.002µs): return nil
                                              Gather#73 ends at 970.482µs
                                          Combine#112 step 3/4 (+517ns): 480ns self time
                                          Combine#112 step 4/4 (+997ns): return error
                                          Combine#112 ends at 21.623µs
                                      Gather#113 step 3/4 (+546ns): 438ns self time
                                      Gather#113 step 4/4 (+984ns): return nil
                                      Gather#113 ends at 11.066µs
                                  Plan#4 step 2/5 (+0s): scatter:
                                    Task#114: pool=0
                                    Task#114 step 1/2 (+0s): 10.044µs self time
                                    Task#114 step 2/2 (+10.044µs): return nil
                                    Task#114 ends at 10.044µs
                                      Gather#114: index=14
                                      Gather#114 step 1/8 (+0s): 94.425µs self time
                                      Gather#114 step 2/8 (+94.425µs): scatter:
                                        Task#107: pool=0
                                        Task#107 step 1/2 (+0s): 10.006µs self time
                                        Task#107 step 2/2 (+10.006µs): return nil
                                        Task#107 ends at 114.475µs
                                          Gather#107: index=9
                                          Gather#107 step 1/2 (+0s): 997.757µs self time
                                          Gather#107 step 2/2 (+997.757µs): return nil
                                          Gather#107 ends at 1.112232ms
                                      Gather#114 step 3/8 (+94.425µs): 94.137µs self time
                                      Gather#114 step 4/8 (+188.562µs): scatter:
                                        Task#111: pool=0
                                        Task#111 step 1/2 (+0s): 10.59µs self time
                                        Task#111 step 2/2 (+10.59µs): return nil
                                        Task#111 ends at 209.196µs
                                          Gather#111: index=1
                                          Gather#111 step 1/6 (+0s): 18ns self time
                                          Gather#111 step 2/6 (+18ns): scatter:
                                            Task#75: pool=0
                                            Task#75 step 1/4 (+0s): 5.027µs self time
                                            Task#75 step 2/4 (+5.027µs): subjob:
                                              Plan#5: pathCount=21 taskCount=29 maxPathDuration=9.019026ms minGatherCount=21 maxGatherCount=66
                                                 TaskPools[0]: TaskPool#23: limit=7
                                                 TaskPools[1]: TaskPool#24: limit=3
                                                 TaskPools[2]: TaskPool#25: limit=2
                                                 CombinerPools[0]: CombinerPool#14: limit=6
                                                 CombinerPools[1]: CombinerPool#15: limit=6
                                                 CombinerPools[2]: CombinerPool#16: limit=7
                                                 CombinerPools[3]: CombinerPool#17: limit=4
                                                 CombinerPools[4]: CombinerPool#18: limit=6
                                                 CombinerPools[5]: CombinerPool#19: limit=5
                                                 Combiners[0]: pool=3
                                                 Combiners[1]: pool=2
                                                 Combiners[2]: pool=0
                                              Plan#5 step 1/9 (+0s): scatter:
                                                Task#94: pool=1
                                                Task#94 step 1/2 (+0s): 1.786µs self time
                                                Task#94 step 2/2 (+1.786µs): return nil
                                                Task#94 ends at 1.786µs
                                                  Gather#94: index=4
                                                  Gather#94 step 1/2 (+0s): 1.003µs self time
                                                  Gather#94 step 2/2 (+1.003µs): return nil
                                                  Gather#94 ends at 2.789µs
                                              Plan#5 step 2/9 (+0s): scatter:
                                                Task#104: pool=1
                                                Task#104 step 1/2 (+0s): 10.075µs self time
                                                Task#104 step 2/2 (+10.075µs): return nil
                                                Task#104 ends at 10.075µs
                                                  Gather#104: index=3
                                                  Gather#104 step 1/6 (+0s): 347ns self time
                                                  Gather#104 step 2/6 (+347ns): scatter:
                                                    Task#78: pool=2
                                                    Task#78 step 1/2 (+0s): 10.472µs self time
                                                    Task#78 step 2/2 (+10.472µs): return nil
                                                    Task#78 ends at 20.894µs
                                                      Combine#78: index=0 flush=<nil>
                                                      Combine#78 step 1/2 (+0s): 375.685µs self time
                                                      Combine#78 step 2/2 (+375.685µs): return nil
                                                      Combine#78 ends at 396.579µs
                                                  Gather#104 step 3/6 (+347ns): 154ns self time
                                                  Gather#104 step 4/6 (+501ns): scatter:
                                                    Task#101: pool=1
                                                    Task#101 step 1/2 (+0s): 9.996µs self time
                                                    Task#101 step 2/2 (+9.996µs): return nil
                                                    Task#101 ends at 20.572µs
                                                      Gather#101: index=3
                                                      Gather#101 step 1/4 (+0s): 492ns self time
                                                      Gather#101 step 2/4 (+492ns): scatter:
                                                        Task#87: pool=0
                                                        Task#87 step 1/2 (+0s): 9.902µs self time
                                                        Task#87 step 2/2 (+9.902µs): return nil
                                                        Task#87 ends at 30.966µs
                                                          Combine#87: index=1 flush=<nil>
                                                          Combine#87 step 1/2 (+0s): 782ns self time
                                                          Combine#87 step 2/2 (+782ns): return nil
                                                          Combine#87 ends at 31.748µs
                                                      Gather#101 step 3/4 (+492ns): 507ns self time
                                                      Gather#101 step 4/4 (+999ns): return nil
                                                      Gather#101 ends at 21.571µs
                                                  Gather#104 step 5/6 (+501ns): 497ns self time
                                                  Gather#104 step 6/6 (+998ns): return nil
                                                  Gather#104 ends at 11.073µs
                                              Plan#5 step 3/9 (+0s): scatter:
                                                Task#103: pool=2
                                                Task#103 step 1/2 (+0s): 120.818µs self time
                                                Task#103 step 2/2 (+120.818µs): return nil
                                                Task#103 ends at 120.818µs
                                                  Gather#103: index=2
                                                  Gather#103 step 1/10 (+0s): 187ns self time
                                                  Gather#103 step 2/10 (+187ns): scatter:
                                                    Task#90: pool=0
                                                    Task#90 step 1/2 (+0s): 9.999µs self time
                                                    Task#90 step 2/2 (+9.999µs): return nil
                                                    Task#90 ends at 131.004µs
                                                      Gather#90: index=0
                                                      Gather#90 step 1/2 (+0s): 1.03µs self time
                                                      Gather#90 step 2/2 (+1.03µs): return nil
                                                      Gather#90 ends at 132.034µs
                                                  Gather#103 step 3/10 (+187ns): 195ns self time
                                                  Gather#103 step 4/10 (+382ns): scatter:
                                                    Task#93: pool=2
                                                    Task#93 step 1/2 (+0s): 10.028µs self time
                                                    Task#93 step 2/2 (+10.028µs): return nil
                                                    Task#93 ends at 131.228µs
                                                      Gather#93: index=0
                                                      Gather#93 step 1/2 (+0s): 993ns self time
                                                      Gather#93 step 2/2 (+993ns): return nil
                                                      Gather#93 ends at 132.221µs
                                                  Gather#103 step 5/10 (+382ns): 203ns self time
                                                  Gather#103 step 6/10 (+585ns): scatter:
                                                    Task#100: pool=0
                                                    Task#100 step 1/2 (+0s): 10.17µs self time
                                                    Task#100 step 2/2 (+10.17µs): return nil
                                                    Task#100 ends at 131.573µs
                                                      Gather#100: index=0
                                                      Gather#100 step 1/4 (+0s): 499.98µs self time
                                                      Gather#100 step 2/4 (+499.98µs): scatter:
                                                        Task#84: pool=1
                                                        Task#84 step 1/2 (+0s): 9.999µs self time
                                                        Task#84 step 2/2 (+9.999µs): return nil
                                                        Task#84 ends at 641.552µs
                                                          Combine#84: index=1 flush=<nil>
                                                          Combine#84 step 1/2 (+0s): 1.002µs self time
                                                          Combine#84 step 2/2 (+1.002µs): return nil
                                                          Combine#84 ends at 642.554µs
                                                      Gather#100 step 3/4 (+499.98µs): 500.02µs self time
                                                      Gather#100 step 4/4 (+1ms): return nil
                                                      Gather#100 ends at 1.131573ms
                                                  Gather#103 step 7/10 (+585ns): 387ns self time
                                                  Gather#103 step 8/10 (+972ns): scatter:
                                                    Task#102: pool=0
                                                    Task#102 step 1/2 (+0s): 8.867711ms self time
                                                    Task#102 step 2/2 (+8.867711ms): return nil
                                                    Task#102 ends at 8.989501ms
                                                      Gather#102: index=1
                                                      Gather#102 step 1/14 (+0s): 881ns self time
                                                      Gather#102 step 2/14 (+881ns): scatter:
                                                        Task#88: pool=2
                                                        Task#88 step 1/2 (+0s): 10.18µs self time
                                                        Task#88 step 2/2 (+10.18µs): return nil
                                                        Task#88 ends at 9.000562ms
                                                          Gather#88: index=2
                                                          Gather#88 step 1/2 (+0s): 1µs self time
                                                          Gather#88 step 2/2 (+1µs): return nil
                                                          Gather#88 ends at 9.001562ms
                                                      Gather#102 step 3/14 (+881ns): 11ns self time
                                                      Gather#102 step 4/14 (+892ns): scatter:
                                                        Task#99: pool=2
                                                        Task#99 step 1/2 (+0s): 5.512µs self time
                                                        Task#99 step 2/2 (+5.512µs): return nil
                                                        Task#99 ends at 8.995905ms
                                                          Gather#99: index=3
                                                          Gather#99 step 1/10 (+0s): 0s self time
                                                          Gather#99 step 2/10 (+0s): scatter:
                                                            Task#82: pool=1
                                                            Task#82 step 1/2 (+0s): 10.436µs self time
                                                            Task#82 step 2/2 (+10.436µs): return nil
                                                            Task#82 ends at 9.006341ms
                                                              Combine#82: index=2 flush=<nil>
                                                              Combine#82 step 1/2 (+0s): 990ns self time
                                                              Combine#82 step 2/2 (+990ns): return error
                                                              Combine#82 ends at 9.007331ms
                                                          Gather#99 step 3/10 (+0s): 243ns self time
                                                          Gather#99 step 4/10 (+243ns): scatter:
                                                            Task#85: pool=1
                                                            Task#85 step 1/2 (+0s): 6.275µs self time
                                                            Task#85 step 2/2 (+6.275µs): return nil
                                                            Task#85 ends at 9.002423ms
                                                              Gather#85: index=1
                                                              Gather#85 step 1/2 (+0s): 1.002µs self time
                                                              Gather#85 step 2/2 (+1.002µs): return nil
                                                              Gather#85 ends at 9.003425ms
                                                          Gather#99 step 5/10 (+243ns): 684ns self time
                                                          Gather#99 step 6/10 (+927ns): scatter:
                                                            Task#83: pool=0
                                                            Task#83 step 1/2 (+0s): 9.984µs self time
                                                            Task#83 step 2/2 (+9.984µs): return nil
                                                            Task#83 ends at 9.006816ms
                                                              Gather#83: index=1
                                                              Gather#83 step 1/2 (+0s): 1.001µs self time
                                                              Gather#83 step 2/2 (+1.001µs): return nil
                                                              Gather#83 ends at 9.007817ms
                                                          Gather#99 step 7/10 (+927ns): 32ns self time
                                                          Gather#99 step 8/10 (+959ns): scatter:
                                                            Task#97: pool=0
                                                            Task#97 step 1/2 (+0s): 9.822µs self time
                                                            Task#97 step 2/2 (+9.822µs): return nil
                                                            Task#97 ends at 9.006686ms
                                                              Gather#97: index=6
                                                              Gather#97 step 1/6 (+0s): 332ns self time
                                                              Gather#97 step 2/6 (+332ns): scatter:
                                                                Task#91: pool=0
                                                                Task#91 step 1/2 (+0s): 11.014µs self time
                                                                Task#91 step 2/2 (+11.014µs): return nil
                                                                Task#91 ends at 9.018032ms
                                                                  Combine#91: index=2 flush=<nil>
                                                                  Combine#91 step 1/2 (+0s): 994ns self time
                                                                  Combine#91 step 2/2 (+994ns): return nil
                                                                  Combine#91 ends at 9.019026ms
                                                              Gather#97 step 3/6 (+332ns): 599ns self time
                                                              Gather#97 step 4/6 (+931ns): scatter:
                                                                Task#89: pool=2
                                                                Task#89 step 1/2 (+0s): 9.993µs self time
                                                                Task#89 step 2/2 (+9.993µs): return nil
                                                                Task#89 ends at 9.01761ms
                                                                  Combine#89: index=0 flush=<nil>
                                                                  Combine#89 step 1/2 (+0s): 996ns self time
                                                                  Combine#89 step 2/2 (+996ns): return nil
                                                                  Combine#89 ends at 9.018606ms
                                                              Gather#97 step 5/6 (+931ns): 71ns self time
                                                              Gather#97 step 6/6 (+1.002µs): return nil
                                                              Gather#97 ends at 9.007688ms
                                                          Gather#99 step 9/10 (+959ns): 31ns self time
                                                          Gather#99 step 10/10 (+990ns): return nil
                                                          Gather#99 ends at 8.996895ms
                                                      Gather#102 step 5/14 (+892ns): 68ns self time
                                                      Gather#102 step 6/14 (+960ns): scatter:
                                                        Task#77: pool=2
                                                        Task#77 step 1/2 (+0s): 9.998µs self time
                                                        Task#77 step 2/2 (+9.998µs): return nil
                                                        Task#77 ends at 9.000459ms
                                                          Combine#77: index=1 flush=<nil>
                                                          Combine#77 step 1/2 (+0s): 1.051µs self time
                                                          Combine#77 step 2/2 (+1.051µs): return nil
                                                          Combine#77 ends at 9.00151ms
                                                      Gather#102 step 7/14 (+960ns): 3ns self time
                                                      Gather#102 step 8/14 (+963ns): scatter:
                                                        Task#98: pool=2
                                                        Task#98 step 1/2 (+0s): 9.969µs self time
                                                        Task#98 step 2/2 (+9.969µs): return nil
                                                        Task#98 ends at 9.000433ms
                                                          Gather#98: index=0
                                                          Gather#98 step 1/4 (+0s): 478ns self time
                                                          Gather#98 step 2/4 (+478ns): scatter:
                                                            Task#76: pool=2
                                                            Task#76 step 1/2 (+0s): 10.013µs self time
                                                            Task#76 step 2/2 (+10.013µs): return nil
                                                            Task#76 ends at 9.010924ms
                                                              Gather#76: index=0
                                                              Gather#76 step 1/2 (+0s): 1.001µs self time
                                                              Gather#76 step 2/2 (+1.001µs): return nil
                                                              Gather#76 ends at 9.011925ms
                                                          Gather#98 step 3/4 (+478ns): 521ns self time
                                                          Gather#98 step 4/4 (+999ns): return nil
                                                          Gather#98 ends at 9.001432ms
                                                      Gather#102 step 9/14 (+963ns): 10ns self time
                                                      Gather#102 step 10/14 (+973ns): scatter:
                                                        Task#86: pool=1
                                                        Task#86 step 1/2 (+0s): 10.028µs self time
                                                        Task#86 step 2/2 (+10.028µs): return nil
                                                        Task#86 ends at 9.000502ms
                                                          Gather#86: index=7
                                                          Gather#86 step 1/2 (+0s): 0s self time
                                                          Gather#86 step 2/2 (+0s): return nil
                                                          Gather#86 ends at 9.000502ms
                                                      Gather#102 step 11/14 (+973ns): 2ns self time
                                                      Gather#102 step 12/14 (+975ns): scatter:
                                                        Task#81: pool=0
                                                        Task#81 step 1/2 (+0s): 6.315µs self time
                                                        Task#81 step 2/2 (+6.315µs): return error
                                                        Task#81 ends at 8.996791ms
                                                          Combine#81: index=0 flush=Gather#81
                                                          Combine#81 step 1/2 (+0s): 997ns self time
                                                          Combine#81 step 2/2 (+997ns): return nil
                                                          Combine#81 ends at 8.997788ms
                                                            Gather#81: index=5
                                                            Gather#81 step 1/2 (+0s): 999ns self time
                                                            Gather#81 step 2/2 (+999ns): return nil
                                                            Gather#81 ends at 0s
                                                      Gather#102 step 13/14 (+975ns): 24ns self time
                                                      Gather#102 step 14/14 (+999ns): return nil
                                                      Gather#102 ends at 8.9905ms
                                                  Gather#103 step 9/10 (+972ns): 26ns self time
                                                  Gather#103 step 10/10 (+998ns): return nil
                                                  Gather#103 ends at 121.816µs
                                              Plan#5 step 4/9 (+0s): scatter:
                                                Task#79: pool=1
                                                Task#79 step 1/2 (+0s): 9.999µs self time
                                                Task#79 step 2/2 (+9.999µs): return nil
                                                Task#79 ends at 9.999µs
                                                  Gather#79: index=5
                                                  Gather#79 step 1/2 (+0s): 595ns self time
                                                  Gather#79 step 2/2 (+595ns): return nil
                                                  Gather#79 ends at 10.594µs
                                              Plan#5 step 5/9 (+0s): scatter:
                                                Task#96: pool=0
                                                Task#96 step 1/2 (+0s): 8.069474ms self time
                                                Task#96 step 2/2 (+8.069474ms): return nil
                                                Task#96 ends at 8.069474ms
                                                  Gather#96: index=4
                                                  Gather#96 step 1/2 (+0s): 999ns self time
                                                  Gather#96 step 2/2 (+999ns): return nil
                                                  Gather#96 ends at 8.070473ms
                                              Plan#5 step 6/9 (+0s): scatter:
                                                Task#92: pool=0
                                                Task#92 step 1/2 (+0s): 9.991µs self time
                                                Task#92 step 2/2 (+9.991µs): return nil
                                                Task#92 ends at 9.991µs
                                                  Gather#92: index=2
                                                  Gather#92 step 1/2 (+0s): 11.985µs self time
                                                  Gather#92 step 2/2 (+11.985µs): return nil
                                                  Gather#92 ends at 21.976µs
                                              Plan#5 step 7/9 (+0s): scatter:
                                                Task#95: pool=1
                                                Task#95 step 1/2 (+0s): 9.997µs self time
                                                Task#95 step 2/2 (+9.997µs): return nil
                                                Task#95 ends at 9.997µs
                                                  Combine#95: index=0 flush=<nil>
                                                  Combine#95 step 1/2 (+0s): 183ns self time
                                                  Combine#95 step 2/2 (+183ns): return nil
                                                  Combine#95 ends at 10.18µs
                                              Plan#5 step 8/9 (+0s): scatter:
                                                Task#80: pool=2
                                                Task#80 step 1/2 (+0s): 10.005µs self time
                                                Task#80 step 2/2 (+10.005µs): return nil
                                                Task#80 ends at 10.005µs
                                                  Gather#80: index=4
                                                  Gather#80 step 1/2 (+0s): 743ns self time
                                                  Gather#80 step 2/2 (+743ns): return error
                                                  Gather#80 ends at 10.748µs
                                              Plan#5 step 9/9 (+0s): ends at 9.019026ms
                                            Task#75 step 3/4 (+9.024053ms): 5.03µs self time
                                            Task#75 step 4/4 (+9.029083ms): return nil
                                            Task#75 ends at 9.238297ms
                                              Combine#75: index=2 flush=<nil>
                                              Combine#75 step 1/2 (+0s): 1.047µs self time
                                              Combine#75 step 2/2 (+1.047µs): return nil
                                              Combine#75 ends at 9.239344ms
                                          Gather#111 step 3/6 (+18ns): 123ns self time
                                          Gather#111 step 4/6 (+141ns): scatter:
                                            Task#110: pool=1
                                            Task#110 step 1/2 (+0s): 10.032µs self time
                                            Task#110 step 2/2 (+10.032µs): return nil
                                            Task#110 ends at 219.369µs
                                              Gather#110: index=0
                                              Gather#110 step 1/6 (+0s): 372ns self time
                                              Gather#110 step 2/6 (+372ns): scatter:
                                                Task#106: pool=1
                                                Task#106 step 1/2 (+0s): 311.402µs self time
                                                Task#106 step 2/2 (+311.402µs): return nil
                                                Task#106 ends at 531.143µs
                                                  Gather#106: index=7
                                                  Gather#106 step 1/2 (+0s): 999ns self time
                                                  Gather#106 step 2/2 (+999ns): return nil
                                                  Gather#106 ends at 532.142µs
                                              Gather#110 step 3/6 (+372ns): 305ns self time
                                              Gather#110 step 4/6 (+677ns): scatter:
                                                Task#109: pool=0
                                                Task#109 step 1/2 (+0s): 9.899µs self time
                                                Task#109 step 2/2 (+9.899µs): return nil
                                                Task#109 ends at 229.945µs
                                                  Gather#109: index=12
                                                  Gather#109 step 1/4 (+0s): 460ns self time
                                                  Gather#109 step 2/4 (+460ns): scatter:
                                                    Task#72: pool=1
                                                    Task#72 step 1/2 (+0s): 9.655µs self time
                                                    Task#72 step 2/2 (+9.655µs): return nil
                                                    Task#72 ends at 240.06µs
                                                      Gather#72: index=0
                                                      Gather#72 step 1/2 (+0s): 996ns self time
                                                      Gather#72 step 2/2 (+996ns): return nil
                                                      Gather#72 ends at 241.056µs
                                                  Gather#109 step 3/4 (+460ns): 542ns self time
                                                  Gather#109 step 4/4 (+1.002µs): return nil
                                                  Gather#109 ends at 230.947µs
                                              Gather#110 step 5/6 (+677ns): 293ns self time
                                              Gather#110 step 6/6 (+970ns): return nil
                                              Gather#110 ends at 220.339µs
                                          Gather#111 step 5/6 (+141ns): 901ns self time
                                          Gather#111 step 6/6 (+1.042µs): return nil
                                          Gather#111 ends at 210.238µs
                                      Gather#114 step 5/8 (+188.562µs): 92.017µs self time
                                      Gather#114 step 6/8 (+280.579µs): scatter:
                                        Task#74: pool=1
                                        Task#74 step 1/2 (+0s): 483ns self time
                                        Task#74 step 2/2 (+483ns): return nil
                                        Task#74 ends at 291.106µs
                                          Gather#74: index=11
                                          Gather#74 step 1/2 (+0s): 1.058µs self time
                                          Gather#74 step 2/2 (+1.058µs): return nil
                                          Gather#74 ends at 292.164µs
                                      Gather#114 step 7/8 (+280.579µs): 95.133µs self time
                                      Gather#114 step 8/8 (+375.712µs): return nil
                                      Gather#114 ends at 385.756µs
                                  Plan#4 step 3/5 (+0s): scatter:
                                    Task#105: pool=0
                                    Task#105 step 1/2 (+0s): 9.996µs self time
                                    Task#105 step 2/2 (+9.996µs): return nil
                                    Task#105 ends at 9.996µs
                                      Gather#105: index=13
                                      Gather#105 step 1/2 (+0s): 1.043µs self time
                                      Gather#105 step 2/2 (+1.043µs): return nil
                                      Gather#105 ends at 11.039µs
                                  Plan#4 step 4/5 (+0s): scatter:
                                    Task#108: pool=1
                                    Task#108 step 1/2 (+0s): 9.548µs self time
                                    Task#108 step 2/2 (+9.548µs): return nil
                                    Task#108 ends at 9.548µs
                                      Gather#108: index=1
                                      Gather#108 step 1/2 (+0s): 970ns self time
                                      Gather#108 step 2/2 (+970ns): return nil
                                      Gather#108 ends at 10.518µs
                                  Plan#4 step 5/5 (+0s): ends at 9.239344ms
                                Task#71 step 3/4 (+9.242521ms): 2.701µs self time
                                Task#71 step 4/4 (+9.245222ms): return nil
                                Task#71 ends at 9.374912ms
                                  Gather#71: index=2
                                  Gather#71 step 1/4 (+0s): 122ns self time
                                  Gather#71 step 2/4 (+122ns): scatter:
                                    Task#6: pool=8
                                    Task#6 step 1/2 (+0s): 10.184µs self time
                                    Task#6 step 2/2 (+10.184µs): return nil
                                    Task#6 ends at 9.385218ms
                                      Gather#6: index=2
                                      Gather#6 step 1/4 (+0s): 69ns self time
                                      Gather#6 step 2/4 (+69ns): subjob:
                                        Plan#2: pathCount=13 taskCount=27 maxPathDuration=4.373299ms minGatherCount=23 maxGatherCount=31
                                           TaskPools[0]: TaskPool#12: limit=5
                                           CombinerPools[0]: CombinerPool#3: limit=1
                                           CombinerPools[1]: CombinerPool#4: limit=10
                                           CombinerPools[2]: CombinerPool#5: limit=2
                                           CombinerPools[3]: CombinerPool#6: limit=5
                                           CombinerPools[4]: CombinerPool#7: limit=4
                                           CombinerPools[5]: CombinerPool#8: limit=1
                                           CombinerPools[6]: CombinerPool#9: limit=1
                                           CombinerPools[7]: CombinerPool#10: limit=1
                                           Combiners[0]: pool=2
                                        Plan#2 step 1/3 (+0s): scatter:
                                          Task#59: pool=0
                                          Task#59 step 1/2 (+0s): 14.537µs self time
                                          Task#59 step 2/2 (+14.537µs): return nil
                                          Task#59 ends at 14.537µs
                                            Gather#59: index=1
                                            Gather#59 step 1/4 (+0s): 498ns self time
                                            Gather#59 step 2/4 (+498ns): scatter:
                                              Task#55: pool=0
                                              Task#55 step 1/2 (+0s): 9.416µs self time
                                              Task#55 step 2/2 (+9.416µs): return nil
                                              Task#55 ends at 24.451µs
                                                Combine#55: index=0 flush=<nil>
                                                Combine#55 step 1/4 (+0s): 66ns self time
                                                Combine#55 step 2/4 (+66ns): scatter:
                                                  Task#8: pool=0
                                                  Task#8 step 1/2 (+0s): 9.998µs self time
                                                  Task#8 step 2/2 (+9.998µs): return nil
                                                  Task#8 ends at 34.515µs
                                                    Gather#8: index=10
                                                    Gather#8 step 1/2 (+0s): 851ns self time
                                                    Gather#8 step 2/2 (+851ns): return nil
                                                    Gather#8 ends at 35.366µs
                                                Combine#55 step 3/4 (+66ns): 835ns self time
                                                Combine#55 step 4/4 (+901ns): return nil
                                                Combine#55 ends at 25.352µs
                                            Gather#59 step 3/4 (+498ns): 500ns self time
                                            Gather#59 step 4/4 (+998ns): return nil
                                            Gather#59 ends at 15.535µs
                                        Plan#2 step 2/3 (+0s): scatter:
                                          Task#60: pool=0
                                          Task#60 step 1/2 (+0s): 9.895µs self time
                                          Task#60 step 2/2 (+9.895µs): return nil
                                          Task#60 ends at 9.895µs
                                            Gather#60: index=18
                                            Gather#60 step 1/18 (+0s): 141ns self time
                                            Gather#60 step 2/18 (+141ns): scatter:
                                              Task#58: pool=0
                                              Task#58 step 1/2 (+0s): 4.076µs self time
                                              Task#58 step 2/2 (+4.076µs): return nil
                                              Task#58 ends at 14.112µs
                                                Gather#58: index=2
                                                Gather#58 step 1/4 (+0s): 529ns self time
                                                Gather#58 step 2/4 (+529ns): scatter:
                                                  Task#52: pool=0
                                                  Task#52 step 1/2 (+0s): 9.97µs self time
                                                  Task#52 step 2/2 (+9.97µs): return nil
                                                  Task#52 ends at 24.611µs
                                                    Gather#52: index=16
                                                    Gather#52 step 1/4 (+0s): 479ns self time
                                                    Gather#52 step 2/4 (+479ns): scatter:
                                                      Task#10: pool=0
                                                      Task#10 step 1/2 (+0s): 2.208µs self time
                                                      Task#10 step 2/2 (+2.208µs): return nil
                                                      Task#10 ends at 27.298µs
                                                        Gather#10: index=4
                                                        Gather#10 step 1/2 (+0s): 1ms self time
                                                        Gather#10 step 2/2 (+1ms): return nil
                                                        Gather#10 ends at 1.027298ms
                                                    Gather#52 step 3/4 (+479ns): 525ns self time
                                                    Gather#52 step 4/4 (+1.004µs): return nil
                                                    Gather#52 ends at 25.615µs
                                                Gather#58 step 3/4 (+529ns): 501ns self time
                                                Gather#58 step 4/4 (+1.03µs): return nil
                                                Gather#58 ends at 15.142µs
                                            Gather#60 step 3/18 (+141ns): 102ns self time
                                            Gather#60 step 4/18 (+243ns): scatter:
                                              Task#56: pool=0
                                              Task#56 step 1/2 (+0s): 9.996µs self time
                                              Task#56 step 2/2 (+9.996µs): return nil
                                              Task#56 ends at 20.134µs
                                                Gather#56: index=18
                                                Gather#56 step 1/4 (+0s): 263ns self time
                                                Gather#56 step 2/4 (+263ns): scatter:
                                                  Task#53: pool=0
                                                  Task#53 step 1/2 (+0s): 32.683µs self time
                                                  Task#53 step 2/2 (+32.683µs): return nil
                                                  Task#53 ends at 53.08µs
                                                    Combine#53: index=0 flush=<nil>
                                                    Combine#53 step 1/6 (+0s): 65ns self time
                                                    Combine#53 step 2/6 (+65ns): scatter:
                                                      Task#21: pool=0
                                                      Task#21 step 1/2 (+0s): 9.996µs self time
                                                      Task#21 step 2/2 (+9.996µs): return nil
                                                      Task#21 ends at 63.141µs
                                                        Gather#21: index=14
                                                        Gather#21 step 1/4 (+0s): 414.18µs self time
                                                        Gather#21 step 2/4 (+414.18µs): scatter:
                                                          Task#15: pool=0
                                                          Task#15 step 1/2 (+0s): 71.13µs self time
                                                          Task#15 step 2/2 (+71.13µs): return nil
                                                          Task#15 ends at 548.451µs
                                                            Gather#15: index=0
                                                            Gather#15 step 1/2 (+0s): 1.039µs self time
                                                            Gather#15 step 2/2 (+1.039µs): return nil
                                                            Gather#15 ends at 549.49µs
                                                        Gather#21 step 3/4 (+414.18µs): 414.191µs self time
                                                        Gather#21 step 4/4 (+828.371µs): return nil
                                                        Gather#21 ends at 891.512µs
                                                    Combine#53 step 3/6 (+65ns): 672ns self time
                                                    Combine#53 step 4/6 (+737ns): scatter:
                                                      Task#50: pool=0
                                                      Task#50 step 1/2 (+0s): 9.993µs self time
                                                      Task#50 step 2/2 (+9.993µs): return nil
                                                      Task#50 ends at 63.81µs
                                                        Gather#50: index=6
                                                        Gather#50 step 1/4 (+0s): 1.077µs self time
                                                        Gather#50 step 2/4 (+1.077µs): scatter:
                                                          Task#19: pool=0
                                                          Task#19 step 1/2 (+0s): 9.996µs self time
                                                          Task#19 step 2/2 (+9.996µs): return nil
                                                          Task#19 ends at 74.883µs
                                                            Gather#19: index=4
                                                            Gather#19 step 1/2 (+0s): 3.095µs self time
                                                            Gather#19 step 2/2 (+3.095µs): return nil
                                                            Gather#19 ends at 77.978µs
                                                        Gather#50 step 3/4 (+1.077µs): 1.125µs self time
                                                        Gather#50 step 4/4 (+2.202µs): return nil
                                                        Gather#50 ends at 66.012µs
                                                    Combine#53 step 5/6 (+737ns): 277ns self time
                                                    Combine#53 step 6/6 (+1.014µs): return nil
                                                    Combine#53 ends at 54.094µs
                                                Gather#56 step 3/4 (+263ns): 871ns self time
                                                Gather#56 step 4/4 (+1.134µs): return nil
                                                Gather#56 ends at 21.268µs
                                            Gather#60 step 5/18 (+243ns): 111ns self time
                                            Gather#60 step 6/18 (+354ns): scatter:
                                              Task#14: pool=0
                                              Task#14 step 1/2 (+0s): 12.685µs self time
                                              Task#14 step 2/2 (+12.685µs): return nil
                                              Task#14 ends at 22.934µs
                                                Gather#14: index=0
                                                Gather#14 step 1/2 (+0s): 1µs self time
                                                Gather#14 step 2/2 (+1µs): return nil
                                                Gather#14 ends at 23.934µs
                                            Gather#60 step 7/18 (+354ns): 568ns self time
                                            Gather#60 step 8/18 (+922ns): scatter:
                                              Task#18: pool=0
                                              Task#18 step 1/2 (+0s): 9.999µs self time
                                              Task#18 step 2/2 (+9.999µs): return nil
                                              Task#18 ends at 20.816µs
                                                Gather#18: index=3
                                                Gather#18 step 1/2 (+0s): 1.505µs self time
                                                Gather#18 step 2/2 (+1.505µs): return nil
                                                Gather#18 ends at 22.321µs
                                            Gather#60 step 9/18 (+922ns): 16ns self time
                                            Gather#60 step 10/18 (+938ns): scatter:
                                              Task#57: pool=0
                                              Task#57 step 1/2 (+0s): 9.772µs self time
                                              Task#57 step 2/2 (+9.772µs): return nil
                                              Task#57 ends at 20.605µs
                                                Gather#57: index=0
                                                Gather#57 step 1/4 (+0s): 687ns self time
                                                Gather#57 step 2/4 (+687ns): scatter:
                                                  Task#51: pool=0
                                                  Task#51 step 1/2 (+0s): 9.998µs self time
                                                  Task#51 step 2/2 (+9.998µs): return nil
                                                  Task#51 ends at 31.29µs
                                                    Combine#51: index=0 flush=<nil>
                                                    Combine#51 step 1/10 (+0s): 211ns self time
                                                    Combine#51 step 2/10 (+211ns): scatter:
                                                      Task#22: pool=0
                                                      Task#22 step 1/4 (+0s): 1.977µs self time
                                                      Task#22 step 2/4 (+1.977µs): subjob:
                                                        Plan#3: pathCount=15 taskCount=27 maxPathDuration=4.317739ms minGatherCount=24 maxGatherCount=39
                                                           TaskPools[0]: TaskPool#13: limit=3
                                                           TaskPools[1]: TaskPool#14: limit=4
                                                           TaskPools[2]: TaskPool#15: limit=7
                                                           TaskPools[3]: TaskPool#16: limit=2
                                                           TaskPools[4]: TaskPool#17: limit=1
                                                           TaskPools[5]: TaskPool#18: limit=3
                                                           TaskPools[6]: TaskPool#19: limit=4
                                                           TaskPools[7]: TaskPool#20: limit=1
                                                           CombinerPools[0]: CombinerPool#11: limit=5
                                                           Combiners[0]: pool=0
                                                           Combiners[1]: pool=0
                                                           Combiners[2]: pool=0
                                                           Combiners[3]: pool=0
                                                        Plan#3 step 1/11 (+0s): scatter:
                                                          Task#34: pool=0
                                                          Task#34 step 1/2 (+0s): 8.177µs self time
                                                          Task#34 step 2/2 (+8.177µs): return nil
                                                          Task#34 ends at 8.177µs
                                                            Gather#34: index=1
                                                            Gather#34 step 1/2 (+0s): 985ns self time
                                                            Gather#34 step 2/2 (+985ns): return nil
                                                            Gather#34 ends at 9.162µs
                                                        Plan#3 step 2/11 (+0s): scatter:
                                                          Task#49: pool=0
                                                          Task#49 step 1/2 (+0s): 2.157µs self time
                                                          Task#49 step 2/2 (+2.157µs): return nil
                                                          Task#49 ends at 2.157µs
                                                            Gather#49: index=1
                                                            Gather#49 step 1/8 (+0s): 459ns self time
                                                            Gather#49 step 2/8 (+459ns): scatter:
                                                              Task#26: pool=6
                                                              Task#26 step 1/2 (+0s): 6.025µs self time
                                                              Task#26 step 2/2 (+6.025µs): return nil
                                                              Task#26 ends at 8.641µs
                                                                Gather#26: index=3
                                                                Gather#26 step 1/2 (+0s): 1.001µs self time
                                                                Gather#26 step 2/2 (+1.001µs): return nil
                                                                Gather#26 ends at 9.642µs
                                                            Gather#49 step 3/8 (+459ns): 260ns self time
                                                            Gather#49 step 4/8 (+719ns): scatter:
                                                              Task#43: pool=0
                                                              Task#43 step 1/2 (+0s): 4.270827ms self time
                                                              Task#43 step 2/2 (+4.270827ms): return nil
                                                              Task#43 ends at 4.273703ms
                                                                Gather#43: index=0
                                                                Gather#43 step 1/4 (+0s): 656ns self time
                                                                Gather#43 step 2/4 (+656ns): scatter:
                                                                  Task#40: pool=0
                                                                  Task#40 step 1/2 (+0s): 31.14µs self time
                                                                  Task#40 step 2/2 (+31.14µs): return error
                                                                  Task#40 ends at 4.305499ms
                                                                    Combine#40: index=3 flush=<nil>
                                                                    Combine#40 step 1/4 (+0s): 1.288µs self time
                                                                    Combine#40 step 2/4 (+1.288µs): scatter:
                                                                      Task#37: pool=7
                                                                      Task#37 step 1/2 (+0s): 9.955µs self time
                                                                      Task#37 step 2/2 (+9.955µs): return nil
                                                                      Task#37 ends at 4.316742ms
                                                                        Combine#37: index=3 flush=<nil>
                                                                        Combine#37 step 1/2 (+0s): 997ns self time
                                                                        Combine#37 step 2/2 (+997ns): return nil
                                                                        Combine#37 ends at 4.317739ms
                                                                    Combine#40 step 3/4 (+1.288µs): 1.29µs self time
                                                                    Combine#40 step 4/4 (+2.578µs): return nil
                                                                    Combine#40 ends at 4.308077ms
                                                                Gather#43 step 3/4 (+656ns): 86ns self time
                                                                Gather#43 step 4/4 (+742ns): return nil
                                                                Gather#43 ends at 4.274445ms
                                                            Gather#49 step 5/8 (+719ns): 211ns self time
                                                            Gather#49 step 6/8 (+930ns): scatter:
                                                              Task#31: pool=7
                                                              Task#31 step 1/2 (+0s): 10.489µs self time
                                                              Task#31 step 2/2 (+10.489µs): return nil
                                                              Task#31 ends at 13.576µs
                                                                Gather#31: index=1
                                                                Gather#31 step 1/2 (+0s): 999ns self time
                                                                Gather#31 step 2/2 (+999ns): return nil
                                                                Gather#31 ends at 14.575µs
                                                            Gather#49 step 7/8 (+930ns): 926ns self time
                                                            Gather#49 step 8/8 (+1.856µs): return nil
                                                            Gather#49 ends at 4.013µs
                                                        Plan#3 step 3/11 (+0s): scatter:
                                                          Task#36: pool=3
                                                          Task#36 step 1/2 (+0s): 69.594µs self time
                                                          Task#36 step 2/2 (+69.594µs): return nil
                                                          Task#36 ends at 69.594µs
                                                            Gather#36: index=1
                                                            Gather#36 step 1/2 (+0s): 1.007µs self time
                                                            Gather#36 step 2/2 (+1.007µs): return nil
                                                            Gather#36 ends at 70.601µs
                                                        Plan#3 step 4/11 (+0s): scatter:
                                                          Task#35: pool=0
                                                          Task#35 step 1/2 (+0s): 9.994µs self time
                                                          Task#35 step 2/2 (+9.994µs): return nil
                                                          Task#35 ends at 9.994µs
                                                            Gather#35: index=0
                                                            Gather#35 step 1/2 (+0s): 651.102µs self time
                                                            Gather#35 step 2/2 (+651.102µs): return nil
                                                            Gather#35 ends at 661.096µs
                                                        Plan#3 step 5/11 (+0s): scatter:
                                                          Task#25: pool=0
                                                          Task#25 step 1/2 (+0s): 75.042µs self time
                                                          Task#25 step 2/2 (+75.042µs): return nil
                                                          Task#25 ends at 75.042µs
                                                            Gather#25: index=2
                                                            Gather#25 step 1/2 (+0s): 1.001µs self time
                                                            Gather#25 step 2/2 (+1.001µs): return nil
                                                            Gather#25 ends at 76.043µs
                                                        Plan#3 step 6/11 (+0s): scatter:
                                                          Task#47: pool=7
                                                          Task#47 step 1/2 (+0s): 9.976µs self time
                                                          Task#47 step 2/2 (+9.976µs): return nil
                                                          Task#47 ends at 9.976µs
                                                            Gather#47: index=1
                                                            Gather#47 step 1/6 (+0s): 34ns self time
                                                            Gather#47 step 2/6 (+34ns): scatter:
                                                              Task#24: pool=2
                                                              Task#24 step 1/2 (+0s): 9.947µs self time
                                                              Task#24 step 2/2 (+9.947µs): return nil
                                                              Task#24 ends at 19.957µs
                                                                Gather#24: index=3
                                                                Gather#24 step 1/2 (+0s): 1.961µs self time
                                                                Gather#24 step 2/2 (+1.961µs): return nil
                                                                Gather#24 ends at 21.918µs
                                                            Gather#47 step 3/6 (+34ns): 797ns self time
                                                            Gather#47 step 4/6 (+831ns): scatter:
                                                              Task#42: pool=1
                                                              Task#42 step 1/2 (+0s): 9.999µs self time
                                                              Task#42 step 2/2 (+9.999µs): return nil
                                                              Task#42 ends at 20.806µs
                                                                Gather#42: index=0
                                                                Gather#42 step 1/4 (+0s): 750ns self time
                                                                Gather#42 step 2/4 (+750ns): scatter:
                                                                  Task#27: pool=2
                                                                  Task#27 step 1/2 (+0s): 10.392µs self time
                                                                  Task#27 step 2/2 (+10.392µs): return nil
                                                                  Task#27 ends at 31.948µs
                                                                    Gather#27: index=3
                                                                    Gather#27 step 1/2 (+0s): 458.476µs self time
                                                                    Gather#27 step 2/2 (+458.476µs): return nil
                                                                    Gather#27 ends at 490.424µs
                                                                Gather#42 step 3/4 (+750ns): 248ns self time
                                                                Gather#42 step 4/4 (+998ns): return nil
                                                                Gather#42 ends at 21.804µs
                                                            Gather#47 step 5/6 (+831ns): 159ns self time
                                                            Gather#47 step 6/6 (+990ns): return nil
                                                            Gather#47 ends at 10.966µs
                                                        Plan#3 step 7/11 (+0s): scatter:
                                                          Task#23: pool=7
                                                          Task#23 step 1/2 (+0s): 9.999µs self time
                                                          Task#23 step 2/2 (+9.999µs): return nil
                                                          Task#23 ends at 9.999µs
                                                            Gather#23: index=2
                                                            Gather#23 step 1/2 (+0s): 998ns self time
                                                            Gather#23 step 2/2 (+998ns): return nil
                                                            Gather#23 ends at 10.997µs
                                                        Plan#3 step 8/11 (+0s): scatter:
                                                          Task#46: pool=0
                                                          Task#46 step 1/2 (+0s): 9.998µs self time
                                                          Task#46 step 2/2 (+9.998µs): return nil
                                                          Task#46 ends at 9.998µs
                                                            Gather#46: index=3
                                                            Gather#46 step 1/4 (+0s): 548ns self time
                                                            Gather#46 step 2/4 (+548ns): scatter:
                                                              Task#44: pool=6
                                                              Task#44 step 1/2 (+0s): 9.985µs self time
                                                              Task#44 step 2/2 (+9.985µs): return nil
                                                              Task#44 ends at 20.531µs
                                                                Gather#44: index=3
                                                                Gather#44 step 1/6 (+0s): 334ns self time
                                                                Gather#44 step 2/6 (+334ns): scatter:
                                                                  Task#41: pool=2
                                                                  Task#41 step 1/2 (+0s): 10.001µs self time
                                                                  Task#41 step 2/2 (+10.001µs): return nil
                                                                  Task#41 ends at 30.866µs
                                                                    Gather#41: index=2
                                                                    Gather#41 step 1/4 (+0s): 502ns self time
                                                                    Gather#41 step 2/4 (+502ns): scatter:
                                                                      Task#29: pool=6
                                                                      Task#29 step 1/2 (+0s): 11.061µs self time
                                                                      Task#29 step 2/2 (+11.061µs): return nil
                                                                      Task#29 ends at 42.429µs
                                                                        Gather#29: index=3
                                                                        Gather#29 step 1/2 (+0s): 984ns self time
                                                                        Gather#29 step 2/2 (+984ns): return nil
                                                                        Gather#29 ends at 43.413µs
                                                                    Gather#41 step 3/4 (+502ns): 503ns self time
                                                                    Gather#41 step 4/4 (+1.005µs): return nil
                                                                    Gather#41 ends at 31.871µs
                                                                Gather#44 step 3/6 (+334ns): 44ns self time
                                                                Gather#44 step 4/6 (+378ns): scatter:
                                                                  Task#39: pool=2
                                                                  Task#39 step 1/2 (+0s): 9.993µs self time
                                                                  Task#39 step 2/2 (+9.993µs): return nil
                                                                  Task#39 ends at 30.902µs
                                                                    Gather#39: index=3
                                                                    Gather#39 step 1/4 (+0s): 651ns self time
                                                                    Gather#39 step 2/4 (+651ns): scatter:
                                                                      Task#38: pool=3
                                                                      Task#38 step 1/2 (+0s): 9.763µs self time
                                                                      Task#38 step 2/2 (+9.763µs): return nil
                                                                      Task#38 ends at 41.316µs
                                                                        Combine#38: index=0 flush=Gather#38
                                                                        Combine#38 step 1/2 (+0s): 1.04µs self time
                                                                        Combine#38 step 2/2 (+1.04µs): return nil
                                                                        Combine#38 ends at 42.356µs
                                                                          Gather#38: index=2
                                                                          Gather#38 step 1/6 (+0s): 847ns self time
                                                                          Gather#38 step 2/6 (+847ns): scatter:
                                                                            Task#33: pool=4
                                                                            Task#33 step 1/2 (+0s): 10.491µs self time
                                                                            Task#33 step 2/2 (+10.491µs): return nil
                                                                            Task#33 ends at 0s
                                                                              Gather#33: index=3
                                                                              Gather#33 step 1/2 (+0s): 1.003µs self time
                                                                              Gather#33 step 2/2 (+1.003µs): return nil
                                                                              Gather#33 ends at 0s
                                                                          Gather#38 step 3/6 (+847ns): 30ns self time
                                                                          Gather#38 step 4/6 (+877ns): scatter:
                                                                            Task#32: pool=0
                                                                            Task#32 step 1/2 (+0s): 10.025µs self time
                                                                            Task#32 step 2/2 (+10.025µs): return nil
                                                                            Task#32 ends at 0s
                                                                              Gather#32: index=2
                                                                              Gather#32 step 1/2 (+0s): 972ns self time
                                                                              Gather#32 step 2/2 (+972ns): return nil
                                                                              Gather#32 ends at 0s
                                                                          Gather#38 step 5/6 (+877ns): 107ns self time
                                                                          Gather#38 step 6/6 (+984ns): return nil
                                                                          Gather#38 ends at 0s
                                                                    Gather#39 step 3/4 (+651ns): 649ns self time
                                                                    Gather#39 step 4/4 (+1.3µs): return nil
                                                                    Gather#39 ends at 32.202µs
                                                                Gather#44 step 5/6 (+378ns): 631ns self time
                                                                Gather#44 step 6/6 (+1.009µs): return nil
                                                                Gather#44 ends at 21.54µs
                                                            Gather#46 step 3/4 (+548ns): 609ns self time
                                                            Gather#46 step 4/4 (+1.157µs): return nil
                                                            Gather#46 ends at 11.155µs
                                                        Plan#3 step 9/11 (+0s): scatter:
                                                          Task#28: pool=3
                                                          Task#28 step 1/2 (+0s): 10.001µs self time
                                                          Task#28 step 2/2 (+10.001µs): return error
                                                          Task#28 ends at 10.001µs
                                                            Gather#28: index=0
                                                            Gather#28 step 1/2 (+0s): 88.454µs self time
                                                            Gather#28 step 2/2 (+88.454µs): return nil
                                                            Gather#28 ends at 98.455µs
                                                        Plan#3 step 10/11 (+0s): scatter:
                                                          Task#48: pool=0
                                                          Task#48 step 1/2 (+0s): 9.991µs self time
                                                          Task#48 step 2/2 (+9.991µs): return nil
                                                          Task#48 ends at 9.991µs
                                                            Gather#48: index=2
                                                            Gather#48 step 1/4 (+0s): 309.91µs self time
                                                            Gather#48 step 2/4 (+309.91µs): scatter:
                                                              Task#45: pool=7
                                                              Task#45 step 1/2 (+0s): 364ns self time
                                                              Task#45 step 2/2 (+364ns): return nil
                                                              Task#45 ends at 320.265µs
                                                                Gather#45: index=3
                                                                Gather#45 step 1/4 (+0s): 121.594µs self time
                                                                Gather#45 step 2/4 (+121.594µs): scatter:
                                                                  Task#30: pool=0
                                                                  Task#30 step 1/2 (+0s): 10µs self time
                                                                  Task#30 step 2/2 (+10µs): return nil
                                                                  Task#30 ends at 451.859µs
                                                                    Combine#30: index=3 flush=<nil>
                                                                    Combine#30 step 1/2 (+0s): 1.472µs self time
                                                                    Combine#30 step 2/2 (+1.472µs): return nil
                                                                    Combine#30 ends at 453.331µs
                                                                Gather#45 step 3/4 (+121.594µs): 368.673µs self time
                                                                Gather#45 step 4/4 (+490.267µs): return nil
                                                                Gather#45 ends at 810.532µs
                                                            Gather#48 step 3/4 (+309.91µs): 309.904µs self time
                                                            Gather#48 step 4/4 (+619.814µs): return nil
                                                            Gather#48 ends at 629.805µs
                                                        Plan#3 step 11/11 (+0s): ends at 4.317739ms
                                                      Task#22 step 3/4 (+4.319716ms): 7.873µs self time
                                                      Task#22 step 4/4 (+4.327589ms): return nil
                                                      Task#22 ends at 4.35909ms
                                                        Gather#22: index=4
                                                        Gather#22 step 1/4 (+0s): 55ns self time
                                                        Gather#22 step 2/4 (+55ns): scatter:
                                                          Task#12: pool=0
                                                          Task#12 step 1/2 (+0s): 9.889µs self time
                                                          Task#12 step 2/2 (+9.889µs): return nil
                                                          Task#12 ends at 4.369034ms
                                                            Gather#12: index=0
                                                            Gather#12 step 1/2 (+0s): 4.265µs self time
                                                            Gather#12 step 2/2 (+4.265µs): return nil
                                                            Gather#12 ends at 4.373299ms
                                                        Gather#22 step 3/4 (+55ns): 943ns self time
                                                        Gather#22 step 4/4 (+998ns): return nil
                                                        Gather#22 ends at 4.360088ms
                                                    Combine#51 step 3/10 (+211ns): 387ns self time
                                                    Combine#51 step 4/10 (+598ns): scatter:
                                                      Task#11: pool=0
                                                      Task#11 step 1/2 (+0s): 9.998µs self time
                                                      Task#11 step 2/2 (+9.998µs): return nil
                                                      Task#11 ends at 41.886µs
                                                        Gather#11: index=3
                                                        Gather#11 step 1/2 (+0s): 1µs self time
                                                        Gather#11 step 2/2 (+1µs): return nil
                                                        Gather#11 ends at 42.886µs
                                                    Combine#51 step 5/10 (+598ns): 84ns self time
                                                    Combine#51 step 6/10 (+682ns): scatter:
                                                      Task#9: pool=0
                                                      Task#9 step 1/2 (+0s): 10.022µs self time
                                                      Task#9 step 2/2 (+10.022µs): return nil
                                                      Task#9 ends at 41.994µs
                                                        Gather#9: index=2
                                                        Gather#9 step 1/2 (+0s): 512ns self time
                                                        Gather#9 step 2/2 (+512ns): return nil
                                                        Gather#9 ends at 42.506µs
                                                    Combine#51 step 7/10 (+682ns): 140ns self time
                                                    Combine#51 step 8/10 (+822ns): scatter:
                                                      Task#20: pool=0
                                                      Task#20 step 1/2 (+0s): 10.002µs self time
                                                      Task#20 step 2/2 (+10.002µs): return nil
                                                      Task#20 ends at 42.114µs
                                                        Gather#20: index=7
                                                        Gather#20 step 1/4 (+0s): 513ns self time
                                                        Gather#20 step 2/4 (+513ns): scatter:
                                                          Task#7: pool=0
                                                          Task#7 step 1/2 (+0s): 10.008µs self time
                                                          Task#7 step 2/2 (+10.008µs): return nil
                                                          Task#7 ends at 52.635µs
                                                            Gather#7: index=3
                                                            Gather#7 step 1/2 (+0s): 601ns self time
                                                            Gather#7 step 2/2 (+601ns): return error
                                                            Gather#7 ends at 53.236µs
                                                        Gather#20 step 3/4 (+513ns): 487ns self time
                                                        Gather#20 step 4/4 (+1µs): return nil
                                                        Gather#20 ends at 43.114µs
                                                    Combine#51 step 9/10 (+822ns): 54ns self time
                                                    Combine#51 step 10/10 (+876ns): return nil
                                                    Combine#51 ends at 32.166µs
                                                Gather#57 step 3/4 (+687ns): 136ns self time
                                                Gather#57 step 4/4 (+823ns): return nil
                                                Gather#57 ends at 21.428µs
                                            Gather#60 step 11/18 (+938ns): 20ns self time
                                            Gather#60 step 12/18 (+958ns): scatter:
                                              Task#16: pool=0
                                              Task#16 step 1/2 (+0s): 0s self time
                                              Task#16 step 2/2 (+0s): return nil
                                              Task#16 ends at 10.853µs
                                                Gather#16: index=1
                                                Gather#16 step 1/2 (+0s): 1.002µs self time
                                                Gather#16 step 2/2 (+1.002µs): return nil
                                                Gather#16 ends at 11.855µs
                                            Gather#60 step 13/18 (+958ns): 50ns self time
                                            Gather#60 step 14/18 (+1.008µs): scatter:
                                              Task#54: pool=0
                                              Task#54 step 1/2 (+0s): 3.445µs self time
                                              Task#54 step 2/2 (+3.445µs): return nil
                                              Task#54 ends at 14.348µs
                                                Gather#54: index=1
                                                Gather#54 step 1/4 (+0s): 468ns self time
                                                Gather#54 step 2/4 (+468ns): scatter:
                                                  Task#17: pool=0
                                                  Task#17 step 1/2 (+0s): 1.843416ms self time
                                                  Task#17 step 2/2 (+1.843416ms): return nil
                                                  Task#17 ends at 1.858232ms
                                                    Combine#17: index=0 flush=<nil>
                                                    Combine#17 step 1/2 (+0s): 886ns self time
                                                    Combine#17 step 2/2 (+886ns): return nil
                                                    Combine#17 ends at 1.859118ms
                                                Gather#54 step 3/4 (+468ns): 532ns self time
                                                Gather#54 step 4/4 (+1µs): return nil
                                                Gather#54 ends at 15.348µs
                                            Gather#60 step 15/18 (+1.008µs): 1ns self time
                                            Gather#60 step 16/18 (+1.009µs): scatter:
                                              Task#13: pool=0
                                              Task#13 step 1/2 (+0s): 32.201µs self time
                                              Task#13 step 2/2 (+32.201µs): return nil
                                              Task#13 ends at 43.105µs
                                                Gather#13: index=13
                                                Gather#13 step 1/2 (+0s): 999ns self time
                                                Gather#13 step 2/2 (+999ns): return nil
                                                Gather#13 ends at 44.104µs
                                            Gather#60 step 17/18 (+1.009µs): 4ns self time
                                            Gather#60 step 18/18 (+1.013µs): return error
                                            Gather#60 ends at 10.908µs
                                        Plan#2 step 3/3 (+0s): ends at 4.373299ms
                                      Gather#6 step 3/4 (+4.373368ms): 268ns self time
                                      Gather#6 step 4/4 (+4.373636ms): return nil
                                      Gather#6 ends at 13.758854ms
                                  Gather#71 step 3/4 (+122ns): 99ns self time
                                  Gather#71 step 4/4 (+221ns): return nil
                                  Gather#71 ends at 9.375133ms
                              Gather#116 step 5/6 (+53.425µs): 28.568µs self time
                              Gather#116 step 6/6 (+81.993µs): return nil
                              Gather#116 ends at 158.258µs
                          Combine#120 step 5/10 (+2.451µs): 32ns self time
                          Combine#120 step 6/10 (+2.483µs): scatter:
                            Task#65: pool=8
                            Task#65 step 1/2 (+0s): 7.562µs self time
                            Task#65 step 2/2 (+7.562µs): return nil
                            Task#65 ends at 73.885µs
                              Gather#65: index=2
                              Gather#65 step 1/2 (+0s): 1.003µs self time
                              Gather#65 step 2/2 (+1.003µs): return nil
                              Gather#65 ends at 74.888µs
                          Combine#120 step 7/10 (+2.483µs): 1.836µs self time
                          Combine#120 step 8/10 (+4.319µs): scatter:
                            Task#61: pool=6
                            Task#61 step 1/2 (+0s): 9.941µs self time
                            Task#61 step 2/2 (+9.941µs): return nil
                            Task#61 ends at 78.1µs
                              Gather#61: index=4
                              Gather#61 step 1/2 (+0s): 781ns self time
                              Gather#61 step 2/2 (+781ns): return nil
                              Gather#61 ends at 78.881µs
                          Combine#120 step 9/10 (+4.319µs): 1.839µs self time
                          Combine#120 step 10/10 (+6.158µs): return nil
                          Combine#120 ends at 69.998µs
                      Gather#122 step 7/8 (+45.53µs): 16.364µs self time
                      Gather#122 step 8/8 (+61.894µs): return nil
                      Gather#122 ends at 71.845µs
                  Plan#1 step 3/6 (+0s): scatter:
                    Task#3: pool=7
                    Task#3 step 1/2 (+0s): 10.082µs self time
                    Task#3 step 2/2 (+10.082µs): return nil
                    Task#3 ends at 10.082µs
                      Gather#3: index=2
                      Gather#3 step 1/2 (+0s): 1.015µs self time
                      Gather#3 step 2/2 (+1.015µs): return nil
                      Gather#3 ends at 11.097µs
                  Plan#1 step 4/6 (+0s): scatter:
                    Task#125: pool=4
                    Task#125 step 1/2 (+0s): 9.999µs self time
                    Task#125 step 2/2 (+9.999µs): return nil
                    Task#125 ends at 9.999µs
                      Gather#125: index=0
                      Gather#125 step 1/4 (+0s): 3.228µs self time
                      Gather#125 step 2/4 (+3.228µs): scatter:
                        Task#64: pool=0
                        Task#64 step 1/2 (+0s): 1.529821ms self time
                        Task#64 step 2/2 (+1.529821ms): return nil
                        Task#64 ends at 1.543048ms
                          Combine#64: index=1 flush=<nil>
                          Combine#64 step 1/2 (+0s): 1.007µs self time
                          Combine#64 step 2/2 (+1.007µs): return nil
                          Combine#64 ends at 1.544055ms
                      Gather#125 step 3/4 (+3.228µs): 3.333µs self time
                      Gather#125 step 4/4 (+6.561µs): return nil
                      Gather#125 ends at 16.56µs
                  Plan#1 step 5/6 (+0s): scatter:
                    Task#124: pool=0
                    Task#124 step 1/2 (+0s): 9.971µs self time
                    Task#124 step 2/2 (+9.971µs): return nil
                    Task#124 ends at 9.971µs
                      Combine#124: index=3 flush=<nil>
                      Combine#124 step 1/8 (+0s): 999ns self time
                      Combine#124 step 2/8 (+999ns): scatter:
                        Task#62: pool=2
                        Task#62 step 1/2 (+0s): 2.073313ms self time
                        Task#62 step 2/2 (+2.073313ms): return nil
                        Task#62 ends at 2.084283ms
                          Gather#62: index=3
                          Gather#62 step 1/2 (+0s): 800.596µs self time
                          Gather#62 step 2/2 (+800.596µs): return nil
                          Gather#62 ends at 2.884879ms
                      Combine#124 step 3/8 (+999ns): 0s self time
                      Combine#124 step 4/8 (+999ns): scatter:
                        Task#69: pool=9
                        Task#69 step 1/2 (+0s): 9.904µs self time
                        Task#69 step 2/2 (+9.904µs): return nil
                        Task#69 ends at 20.874µs
                          Combine#69: index=3 flush=<nil>
                          Combine#69 step 1/2 (+0s): 687ns self time
                          Combine#69 step 2/2 (+687ns): return nil
                          Combine#69 ends at 21.561µs
                      Combine#124 step 5/8 (+999ns): 0s self time
                      Combine#124 step 6/8 (+999ns): scatter:
                        Task#67: pool=7
                        Task#67 step 1/2 (+0s): 9.998µs self time
                        Task#67 step 2/2 (+9.998µs): return nil
                        Task#67 ends at 20.968µs
                          Gather#67: index=0
                          Gather#67 step 1/2 (+0s): 999ns self time
                          Gather#67 step 2/2 (+999ns): return nil
                          Gather#67 ends at 21.967µs
                      Combine#124 step 7/8 (+999ns): 0s self time
                      Combine#124 step 8/8 (+999ns): return nil
                      Combine#124 ends at 10.97µs
                  Plan#1 step 6/6 (+0s): ends at 13.758854ms
                Gather#0 step 3/4 (+13.759359ms): 516ns self time
                Gather#0 step 4/4 (+13.759875ms): return nil
                Gather#0 ends at 13.797241ms
            Gather#1055 step 5/6 (+198ns): 399ns self time
            Gather#1055 step 6/6 (+597ns): return nil
            Gather#1055 ends at 27.556µs
        Gather#1056 step 9/12 (+629ns): 183ns self time
        Gather#1056 step 10/12 (+812ns): scatter:
          Task#130: pool=1
          Task#130 step 1/2 (+0s): 9.992µs self time
          Task#130 step 2/2 (+9.992µs): return nil
          Task#130 ends at 27.921µs
            Combine#130: index=8 flush=<nil>
            Combine#130 step 1/2 (+0s): 326ns self time
            Combine#130 step 2/2 (+326ns): return nil
            Combine#130 ends at 28.247µs
        Gather#1056 step 11/12 (+812ns): 189ns self time
        Gather#1056 step 12/12 (+1.001µs): return nil
        Gather#1056 ends at 18.118µs
    Combine#1057 step 3/4 (+358ns): 318ns self time
    Combine#1057 step 4/4 (+676ns): return nil
    Combine#1057 ends at 10.681µs
Plan#0 step 4/5 (+0s): scatter:
  Task#1059: pool=1
  Task#1059 step 1/2 (+0s): 11.656µs self time
  Task#1059 step 2/2 (+11.656µs): return nil
  Task#1059 ends at 11.656µs
    Gather#1059: index=0
    Gather#1059 step 1/4 (+0s): 320ns self time
    Gather#1059 step 2/4 (+320ns): scatter:
      Task#127: pool=0
      Task#127 step 1/2 (+0s): 9.968µs self time
      Task#127 step 2/2 (+9.968µs): return nil
      Task#127 ends at 21.944µs
        Gather#127: index=0
        Gather#127 step 1/2 (+0s): 1.037µs self time
        Gather#127 step 2/2 (+1.037µs): return nil
        Gather#127 ends at 22.981µs
    Gather#1059 step 3/4 (+320ns): 672ns self time
    Gather#1059 step 4/4 (+992ns): return nil
    Gather#1059 ends at 12.648µs
Plan#0 step 5/5 (+0s): ends at 35.186905ms`

	ranOnce := false
	rapid.Check(t, func(t *rapid.T) {
		if ranOnce {
			return
		}
		ranOnce = true
		plan := sim.NewPlan(t, &sim.DefaultConfig)
		assert.Equal(t, expected, fmt.Sprintf("%#v", plan), "use -test.v -rapid.v -rapid.log to see full error")
	})
}
