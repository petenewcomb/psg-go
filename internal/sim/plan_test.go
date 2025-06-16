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

	expected := `Plan#0: pathCount=4 taskCount=7 maxPathDuration=281.799482ms minGatherCount=7 maxGatherCount=7
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
Plan#0 step 1/2 (+0s): scatter:
  Task#327: pool=0
  Task#327 step 1/2 (+0s): 99.996µs self time
  Task#327 step 2/2 (+99.996µs): return nil
  Task#327 ends at 99.996µs
    Gather#327: index=5
    Gather#327 step 1/8 (+0s): 2.39µs self time
    Gather#327 step 2/8 (+2.39µs): scatter:
      Task#0: pool=1
      Task#0 step 1/2 (+0s): 100.053µs self time
      Task#0 step 2/2 (+100.053µs): return nil
      Task#0 ends at 202.439µs
        Gather#0: index=3
        Gather#0 step 1/4 (+0s): 5.011µs self time
        Gather#0 step 2/4 (+5.011µs): subjob:
          Plan#1: pathCount=10 taskCount=28 maxPathDuration=281.587043ms minGatherCount=26 maxGatherCount=28
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
          Plan#1 step 1/7 (+0s): scatter:
            Task#146: pool=7
            Task#146 step 1/2 (+0s): 96.363µs self time
            Task#146 step 2/2 (+96.363µs): return nil
            Task#146 ends at 96.363µs
              Combine#146: index=0 flush=<nil>
              Combine#146 step 1/8 (+0s): 2.403µs self time
              Combine#146 step 2/8 (+2.403µs): scatter:
                Task#143: pool=1
                Task#143 step 1/2 (+0s): 100.003µs self time
                Task#143 step 2/2 (+100.003µs): return nil
                Task#143 ends at 198.769µs
                  Gather#143: index=4
                  Gather#143 step 1/6 (+0s): 3.33µs self time
                  Gather#143 step 2/6 (+3.33µs): scatter:
                    Task#6: pool=7
                    Task#6 step 1/2 (+0s): 66.359µs self time
                    Task#6 step 2/2 (+66.359µs): return nil
                    Task#6 ends at 268.458µs
                      Gather#6: index=1
                      Gather#6 step 1/2 (+0s): 10.196µs self time
                      Gather#6 step 2/2 (+10.196µs): return nil
                      Gather#6 ends at 278.654µs
                  Gather#143 step 3/6 (+3.33µs): 3.324µs self time
                  Gather#143 step 4/6 (+6.654µs): scatter:
                    Task#9: pool=2
                    Task#9 step 1/2 (+0s): 99.425µs self time
                    Task#9 step 2/2 (+99.425µs): return nil
                    Task#9 ends at 304.848µs
                      Gather#9: index=1
                      Gather#9 step 1/2 (+0s): 9.997µs self time
                      Gather#9 step 2/2 (+9.997µs): return nil
                      Gather#9 ends at 314.845µs
                  Gather#143 step 5/6 (+6.654µs): 3.341µs self time
                  Gather#143 step 6/6 (+9.995µs): return nil
                  Gather#143 ends at 208.764µs
              Combine#146 step 3/8 (+2.403µs): 2.373µs self time
              Combine#146 step 4/8 (+4.776µs): scatter:
                Task#145: pool=2
                Task#145 step 1/2 (+0s): 100ms self time
                Task#145 step 2/2 (+100ms): return nil
                Task#145 ends at 100.101139ms
                  Gather#145: index=4
                  Gather#145 step 1/4 (+0s): 7.388µs self time
                  Gather#145 step 2/4 (+7.388µs): scatter:
                    Task#2: pool=0
                    Task#2 step 1/2 (+0s): 100µs self time
                    Task#2 step 2/2 (+100µs): return nil
                    Task#2 ends at 100.208527ms
                      Gather#2: index=1
                      Gather#2 step 1/2 (+0s): 9.895µs self time
                      Gather#2 step 2/2 (+9.895µs): return nil
                      Gather#2 ends at 100.218422ms
                  Gather#145 step 3/4 (+7.388µs): 2.609µs self time
                  Gather#145 step 4/4 (+9.997µs): return nil
                  Gather#145 ends at 100.111136ms
              Combine#146 step 5/8 (+4.776µs): 1.574µs self time
              Combine#146 step 6/8 (+6.35µs): scatter:
                Task#8: pool=9
                Task#8 step 1/2 (+0s): 99.946µs self time
                Task#8 step 2/2 (+99.946µs): return nil
                Task#8 ends at 202.659µs
                  Gather#8: index=2
                  Gather#8 step 1/2 (+0s): 10.317µs self time
                  Gather#8 step 2/2 (+10.317µs): return nil
                  Gather#8 ends at 212.976µs
              Combine#146 step 7/8 (+6.35µs): 3.261µs self time
              Combine#146 step 8/8 (+9.611µs): return nil
              Combine#146 ends at 105.974µs
          Plan#1 step 2/7 (+0s): scatter:
            Task#162: pool=1
            Task#162 step 1/2 (+0s): 99.986µs self time
            Task#162 step 2/2 (+99.986µs): return nil
            Task#162 ends at 99.986µs
              Gather#162: index=2
              Gather#162 step 1/4 (+0s): 5.001µs self time
              Gather#162 step 2/4 (+5.001µs): scatter:
                Task#140: pool=1
                Task#140 step 1/2 (+0s): 100.009µs self time
                Task#140 step 2/2 (+100.009µs): return nil
                Task#140 ends at 204.996µs
                  Gather#140: index=2
                  Gather#140 step 1/4 (+0s): 3.869µs self time
                  Gather#140 step 2/4 (+3.869µs): scatter:
                    Task#5: pool=0
                    Task#5 step 1/2 (+0s): 99.999µs self time
                    Task#5 step 2/2 (+99.999µs): return nil
                    Task#5 ends at 308.864µs
                      Gather#5: index=0
                      Gather#5 step 1/2 (+0s): 9.978µs self time
                      Gather#5 step 2/2 (+9.978µs): return nil
                      Gather#5 ends at 318.842µs
                  Gather#140 step 3/4 (+3.869µs): 782ns self time
                  Gather#140 step 4/4 (+4.651µs): return nil
                  Gather#140 ends at 209.647µs
              Gather#162 step 3/4 (+5.001µs): 4.997µs self time
              Gather#162 step 4/4 (+9.998µs): return nil
              Gather#162 ends at 109.984µs
          Plan#1 step 3/7 (+0s): scatter:
            Task#163: pool=1
            Task#163 step 1/2 (+0s): 100.122µs self time
            Task#163 step 2/2 (+100.122µs): return nil
            Task#163 ends at 100.122µs
              Gather#163: index=1
              Gather#163 step 1/6 (+0s): 0s self time
              Gather#163 step 2/6 (+0s): scatter:
                Task#144: pool=9
                Task#144 step 1/2 (+0s): 100µs self time
                Task#144 step 2/2 (+100µs): return nil
                Task#144 ends at 200.122µs
                  Gather#144: index=2
                  Gather#144 step 1/4 (+0s): 5.026µs self time
                  Gather#144 step 2/4 (+5.026µs): scatter:
                    Task#7: pool=5
                    Task#7 step 1/2 (+0s): 41.035533ms self time
                    Task#7 step 2/2 (+41.035533ms): return nil
                    Task#7 ends at 41.240681ms
                      Gather#7: index=4
                      Gather#7 step 1/2 (+0s): 10.418µs self time
                      Gather#7 step 2/2 (+10.418µs): return nil
                      Gather#7 ends at 41.251099ms
                  Gather#144 step 3/4 (+5.026µs): 4.974µs self time
                  Gather#144 step 4/4 (+10µs): return nil
                  Gather#144 ends at 210.122µs
              Gather#163 step 3/6 (+0s): 0s self time
              Gather#163 step 4/6 (+0s): subjob:
                Plan#9: pathCount=3 taskCount=8 maxPathDuration=82.177351ms minGatherCount=7 maxGatherCount=10
                   TaskPools[0]: TaskPool#37: limit=2
                   TaskPools[1]: TaskPool#38: limit=2
                   CombinerPools[0]: CombinerPool#31: limit=3
                   Combiners[0]: pool=0
                   Combiners[1]: pool=0
                   Combiners[2]: pool=0
                   Combiners[3]: pool=0
                   Combiners[4]: pool=0
                   Combiners[5]: pool=0
                Plan#9 step 1/2 (+0s): scatter:
                  Task#178: pool=0
                  Task#178 step 1/2 (+0s): 99.941µs self time
                  Task#178 step 2/2 (+99.941µs): return nil
                  Task#178 ends at 99.941µs
                    Combine#178: index=0 flush=<nil>
                    Combine#178 step 1/6 (+0s): 0s self time
                    Combine#178 step 2/6 (+0s): scatter:
                      Task#164: pool=1
                      Task#164 step 1/2 (+0s): 99.987µs self time
                      Task#164 step 2/2 (+99.987µs): return nil
                      Task#164 ends at 199.928µs
                        Gather#164: index=1
                        Gather#164 step 1/2 (+0s): 9.999µs self time
                        Gather#164 step 2/2 (+9.999µs): return nil
                        Gather#164 ends at 209.927µs
                    Combine#178 step 3/6 (+0s): 4.819µs self time
                    Combine#178 step 4/6 (+4.819µs): scatter:
                      Task#177: pool=0
                      Task#177 step 1/2 (+0s): 537.76µs self time
                      Task#177 step 2/2 (+537.76µs): return nil
                      Task#177 ends at 642.52µs
                        Gather#177: index=3
                        Gather#177 step 1/6 (+0s): 3.807µs self time
                        Gather#177 step 2/6 (+3.807µs): scatter:
                          Task#169: pool=1
                          Task#169 step 1/4 (+0s): 50.007µs self time
                          Task#169 step 2/4 (+50.007µs): subjob:
                            Plan#10: pathCount=3 taskCount=7 maxPathDuration=81.046744ms minGatherCount=6 maxGatherCount=16
                               TaskPools[0]: TaskPool#39: limit=7
                               TaskPools[1]: TaskPool#40: limit=9
                               CombinerPools[0]: CombinerPool#32: limit=2
                               CombinerPools[1]: CombinerPool#33: limit=1
                               CombinerPools[2]: CombinerPool#34: limit=2
                               CombinerPools[3]: CombinerPool#35: limit=10
                               Combiners[0]: pool=3
                               Combiners[1]: pool=3
                               Combiners[2]: pool=3
                            Plan#10 step 1/2 (+0s): scatter:
                              Task#176: pool=0
                              Task#176 step 1/2 (+0s): 80.606029ms self time
                              Task#176 step 2/2 (+80.606029ms): return nil
                              Task#176 ends at 80.606029ms
                                Gather#176: index=2
                                Gather#176 step 1/8 (+0s): 2.473µs self time
                                Gather#176 step 2/8 (+2.473µs): scatter:
                                  Task#175: pool=0
                                  Task#175 step 1/2 (+0s): 100.382µs self time
                                  Task#175 step 2/2 (+100.382µs): return nil
                                  Task#175 ends at 80.708884ms
                                    Gather#175: index=2
                                    Gather#175 step 1/4 (+0s): 5.03µs self time
                                    Gather#175 step 2/4 (+5.03µs): scatter:
                                      Task#174: pool=1
                                      Task#174 step 1/2 (+0s): 100.001µs self time
                                      Task#174 step 2/2 (+100.001µs): return nil
                                      Task#174 ends at 80.813915ms
                                        Combine#174: index=2 flush=<nil>
                                        Combine#174 step 1/4 (+0s): 59.328µs self time
                                        Combine#174 step 2/4 (+59.328µs): scatter:
                                          Task#173: pool=0
                                          Task#173 step 1/2 (+0s): 54.252µs self time
                                          Task#173 step 2/2 (+54.252µs): return nil
                                          Task#173 ends at 80.927495ms
                                            Gather#173: index=0
                                            Gather#173 step 1/4 (+0s): 4.993µs self time
                                            Gather#173 step 2/4 (+4.993µs): scatter:
                                              Task#170: pool=0
                                              Task#170 step 1/2 (+0s): 72.653µs self time
                                              Task#170 step 2/2 (+72.653µs): return nil
                                              Task#170 ends at 81.005141ms
                                                Gather#170: index=3
                                                Gather#170 step 1/2 (+0s): 41.603µs self time
                                                Gather#170 step 2/2 (+41.603µs): return nil
                                                Gather#170 ends at 81.046744ms
                                            Gather#173 step 3/4 (+4.993µs): 5.006µs self time
                                            Gather#173 step 4/4 (+9.999µs): return error
                                            Gather#173 ends at 80.937494ms
                                        Combine#174 step 3/4 (+59.328µs): 59.33µs self time
                                        Combine#174 step 4/4 (+118.658µs): return nil
                                        Combine#174 ends at 80.932573ms
                                    Gather#175 step 3/4 (+5.03µs): 5.046µs self time
                                    Gather#175 step 4/4 (+10.076µs): return nil
                                    Gather#175 ends at 80.71896ms
                                Gather#176 step 3/8 (+2.473µs): 2.482µs self time
                                Gather#176 step 4/8 (+4.955µs): scatter:
                                  Task#172: pool=0
                                  Task#172 step 1/2 (+0s): 99.983µs self time
                                  Task#172 step 2/2 (+99.983µs): return nil
                                  Task#172 ends at 80.710967ms
                                    Gather#172: index=0
                                    Gather#172 step 1/2 (+0s): 10.376µs self time
                                    Gather#172 step 2/2 (+10.376µs): return nil
                                    Gather#172 ends at 80.721343ms
                                Gather#176 step 5/8 (+4.955µs): 2.501µs self time
                                Gather#176 step 6/8 (+7.456µs): scatter:
                                  Task#171: pool=0
                                  Task#171 step 1/2 (+0s): 107.018µs self time
                                  Task#171 step 2/2 (+107.018µs): return nil
                                  Task#171 ends at 80.720503ms
                                    Gather#171: index=2
                                    Gather#171 step 1/2 (+0s): 9.999µs self time
                                    Gather#171 step 2/2 (+9.999µs): return nil
                                    Gather#171 ends at 80.730502ms
                                Gather#176 step 7/8 (+7.456µs): 2.472µs self time
                                Gather#176 step 8/8 (+9.928µs): return nil
                                Gather#176 ends at 80.615957ms
                            Plan#10 step 2/2 (+0s): ends at 81.046744ms
                          Task#169 step 3/4 (+81.096751ms): 50.009µs self time
                          Task#169 step 4/4 (+81.14676ms): return nil
                          Task#169 ends at 81.793087ms
                            Gather#169: index=4
                            Gather#169 step 1/4 (+0s): 192.103µs self time
                            Gather#169 step 2/4 (+192.103µs): scatter:
                              Task#166: pool=0
                              Task#166 step 1/2 (+0s): 96.757µs self time
                              Task#166 step 2/2 (+96.757µs): return nil
                              Task#166 ends at 82.081947ms
                                Gather#166: index=4
                                Gather#166 step 1/2 (+0s): 7.75µs self time
                                Gather#166 step 2/2 (+7.75µs): return nil
                                Gather#166 ends at 82.089697ms
                            Gather#169 step 3/4 (+192.103µs): 192.161µs self time
                            Gather#169 step 4/4 (+384.264µs): return nil
                            Gather#169 ends at 82.177351ms
                        Gather#177 step 3/6 (+3.807µs): 1.474µs self time
                        Gather#177 step 4/6 (+5.281µs): scatter:
                          Task#168: pool=0
                          Task#168 step 1/2 (+0s): 99.986µs self time
                          Task#168 step 2/2 (+99.986µs): return nil
                          Task#168 ends at 747.787µs
                            Gather#168: index=2
                            Gather#168 step 1/4 (+0s): 5.059µs self time
                            Gather#168 step 2/4 (+5.059µs): scatter:
                              Task#167: pool=0
                              Task#167 step 1/2 (+0s): 99.759µs self time
                              Task#167 step 2/2 (+99.759µs): return nil
                              Task#167 ends at 852.605µs
                                Gather#167: index=0
                                Gather#167 step 1/4 (+0s): 5.008µs self time
                                Gather#167 step 2/4 (+5.008µs): scatter:
                                  Task#165: pool=1
                                  Task#165 step 1/2 (+0s): 100.409µs self time
                                  Task#165 step 2/2 (+100.409µs): return nil
                                  Task#165 ends at 958.022µs
                                    Gather#165: index=2
                                    Gather#165 step 1/2 (+0s): 10.018µs self time
                                    Gather#165 step 2/2 (+10.018µs): return nil
                                    Gather#165 ends at 968.04µs
                                Gather#167 step 3/4 (+5.008µs): 4.979µs self time
                                Gather#167 step 4/4 (+9.987µs): return nil
                                Gather#167 ends at 862.592µs
                            Gather#168 step 3/4 (+5.059µs): 5.064µs self time
                            Gather#168 step 4/4 (+10.123µs): return nil
                            Gather#168 ends at 757.91µs
                        Gather#177 step 5/6 (+5.281µs): 4.351µs self time
                        Gather#177 step 6/6 (+9.632µs): return nil
                        Gather#177 ends at 652.152µs
                    Combine#178 step 5/6 (+4.819µs): 4.813µs self time
                    Combine#178 step 6/6 (+9.632µs): return nil
                    Combine#178 ends at 109.573µs
                Plan#9 step 2/2 (+0s): ends at 82.177351ms
              Gather#163 step 5/6 (+82.177351ms): 0s self time
              Gather#163 step 6/6 (+82.177351ms): return nil
              Gather#163 ends at 82.277473ms
          Plan#1 step 4/7 (+0s): scatter:
            Task#161: pool=2
            Task#161 step 1/2 (+0s): 99.994µs self time
            Task#161 step 2/2 (+99.994µs): return nil
            Task#161 ends at 99.994µs
              Gather#161: index=1
              Gather#161 step 1/4 (+0s): 2.114µs self time
              Gather#161 step 2/4 (+2.114µs): scatter:
                Task#142: pool=8
                Task#142 step 1/2 (+0s): 3.83174ms self time
                Task#142 step 2/2 (+3.83174ms): return nil
                Task#142 ends at 3.933848ms
                  Gather#142: index=2
                  Gather#142 step 1/4 (+0s): 5.093µs self time
                  Gather#142 step 2/4 (+5.093µs): scatter:
                    Task#44: pool=1
                    Task#44 step 1/4 (+0s): 47.216µs self time
                    Task#44 step 2/4 (+47.216µs): subjob:
                      Plan#4: pathCount=2 taskCount=7 maxPathDuration=173.057952ms minGatherCount=6 maxGatherCount=7
                         TaskPools[0]: TaskPool#15: limit=8
                         TaskPools[1]: TaskPool#16: limit=8
                         TaskPools[2]: TaskPool#17: limit=6
                         TaskPools[3]: TaskPool#18: limit=10
                         TaskPools[4]: TaskPool#19: limit=1
                         TaskPools[5]: TaskPool#20: limit=1
                         TaskPools[6]: TaskPool#21: limit=4
                         TaskPools[7]: TaskPool#22: limit=1
                         TaskPools[8]: TaskPool#23: limit=3
                         TaskPools[9]: TaskPool#24: limit=2
                         CombinerPools[0]: CombinerPool#6: limit=10
                         CombinerPools[1]: CombinerPool#7: limit=1
                         Combiners[0]: pool=0
                         Combiners[1]: pool=1
                         Combiners[2]: pool=1
                         Combiners[3]: pool=1
                         Combiners[4]: pool=1
                         Combiners[5]: pool=0
                      Plan#4 step 1/3 (+0s): scatter:
                        Task#138: pool=4
                        Task#138 step 1/2 (+0s): 116.028µs self time
                        Task#138 step 2/2 (+116.028µs): return nil
                        Task#138 ends at 116.028µs
                          Gather#138: index=4
                          Gather#138 step 1/4 (+0s): 6.612µs self time
                          Gather#138 step 2/4 (+6.612µs): scatter:
                            Task#136: pool=1
                            Task#136 step 1/2 (+0s): 99.679µs self time
                            Task#136 step 2/2 (+99.679µs): return nil
                            Task#136 ends at 222.319µs
                              Combine#136: index=1 flush=<nil>
                              Combine#136 step 1/4 (+0s): 5.028µs self time
                              Combine#136 step 2/4 (+5.028µs): scatter:
                                Task#111: pool=1
                                Task#111 step 1/2 (+0s): 100.197µs self time
                                Task#111 step 2/2 (+100.197µs): return nil
                                Task#111 ends at 327.544µs
                                  Gather#111: index=2
                                  Gather#111 step 1/2 (+0s): 9.989µs self time
                                  Gather#111 step 2/2 (+9.989µs): return nil
                                  Gather#111 ends at 337.533µs
                              Combine#136 step 3/4 (+5.028µs): 5.026µs self time
                              Combine#136 step 4/4 (+10.054µs): return nil
                              Combine#136 ends at 232.373µs
                          Gather#138 step 3/4 (+6.612µs): 3.387µs self time
                          Gather#138 step 4/4 (+9.999µs): return nil
                          Gather#138 ends at 126.027µs
                      Plan#4 step 2/3 (+0s): scatter:
                        Task#137: pool=8
                        Task#137 step 1/2 (+0s): 100.003µs self time
                        Task#137 step 2/2 (+100.003µs): return nil
                        Task#137 ends at 100.003µs
                          Gather#137: index=0
                          Gather#137 step 1/4 (+0s): 9.99µs self time
                          Gather#137 step 2/4 (+9.99µs): scatter:
                            Task#113: pool=3
                            Task#113 step 1/2 (+0s): 0s self time
                            Task#113 step 2/2 (+0s): return error
                            Task#113 ends at 109.993µs
                              Gather#113: index=1
                              Gather#113 step 1/6 (+0s): 2.36µs self time
                              Gather#113 step 2/6 (+2.36µs): subjob:
                                Plan#7: pathCount=9 taskCount=22 maxPathDuration=102.49947ms minGatherCount=18 maxGatherCount=29
                                   TaskPools[0]: TaskPool#34: limit=1
                                   CombinerPools[0]: CombinerPool#20: limit=1
                                   CombinerPools[1]: CombinerPool#21: limit=3
                                   CombinerPools[2]: CombinerPool#22: limit=1
                                   CombinerPools[3]: CombinerPool#23: limit=8
                                   Combiners[0]: pool=2
                                   Combiners[1]: pool=0
                                   Combiners[2]: pool=2
                                   Combiners[3]: pool=2
                                   Combiners[4]: pool=1
                                   Combiners[5]: pool=1
                                   Combiners[6]: pool=0
                                   Combiners[7]: pool=2
                                   Combiners[8]: pool=3
                                   Combiners[9]: pool=1
                                   Combiners[10]: pool=3
                                   Combiners[11]: pool=0
                                   Combiners[12]: pool=1
                                   Combiners[13]: pool=0
                                   Combiners[14]: pool=3
                                   Combiners[15]: pool=1
                                   Combiners[16]: pool=0
                                   Combiners[17]: pool=0
                                   Combiners[18]: pool=0
                                Plan#7 step 1/6 (+0s): scatter:
                                  Task#135: pool=0
                                  Task#135 step 1/2 (+0s): 99.928µs self time
                                  Task#135 step 2/2 (+99.928µs): return nil
                                  Task#135 ends at 99.928µs
                                    Gather#135: index=1
                                    Gather#135 step 1/4 (+0s): 2.329µs self time
                                    Gather#135 step 2/4 (+2.329µs): scatter:
                                      Task#127: pool=0
                                      Task#127 step 1/2 (+0s): 99.957µs self time
                                      Task#127 step 2/2 (+99.957µs): return nil
                                      Task#127 ends at 202.214µs
                                        Gather#127: index=1
                                        Gather#127 step 1/4 (+0s): 4.769µs self time
                                        Gather#127 step 2/4 (+4.769µs): scatter:
                                          Task#125: pool=0
                                          Task#125 step 1/2 (+0s): 100.001µs self time
                                          Task#125 step 2/2 (+100.001µs): return nil
                                          Task#125 ends at 306.984µs
                                            Gather#125: index=3
                                            Gather#125 step 1/4 (+0s): 5.025µs self time
                                            Gather#125 step 2/4 (+5.025µs): scatter:
                                              Task#124: pool=0
                                              Task#124 step 1/2 (+0s): 123.535µs self time
                                              Task#124 step 2/2 (+123.535µs): return nil
                                              Task#124 ends at 435.544µs
                                                Combine#124: index=14 flush=<nil>
                                                Combine#124 step 1/6 (+0s): 3.335µs self time
                                                Combine#124 step 2/6 (+3.335µs): scatter:
                                                  Task#123: pool=0
                                                  Task#123 step 1/2 (+0s): 401.798µs self time
                                                  Task#123 step 2/2 (+401.798µs): return nil
                                                  Task#123 ends at 840.677µs
                                                    Gather#123: index=1
                                                    Gather#123 step 1/4 (+0s): 5.063µs self time
                                                    Gather#123 step 2/4 (+5.063µs): scatter:
                                                      Task#121: pool=0
                                                      Task#121 step 1/2 (+0s): 9.19µs self time
                                                      Task#121 step 2/2 (+9.19µs): return nil
                                                      Task#121 ends at 854.93µs
                                                        Gather#121: index=1
                                                        Gather#121 step 1/2 (+0s): 10.016µs self time
                                                        Gather#121 step 2/2 (+10.016µs): return nil
                                                        Gather#121 ends at 864.946µs
                                                    Gather#123 step 3/4 (+5.063µs): 4.933µs self time
                                                    Gather#123 step 4/4 (+9.996µs): return nil
                                                    Gather#123 ends at 850.673µs
                                                Combine#124 step 3/6 (+3.335µs): 3.101µs self time
                                                Combine#124 step 4/6 (+6.436µs): scatter:
                                                  Task#122: pool=0
                                                  Task#122 step 1/2 (+0s): 99.861µs self time
                                                  Task#122 step 2/2 (+99.861µs): return nil
                                                  Task#122 ends at 541.841µs
                                                    Gather#122: index=1
                                                    Gather#122 step 1/2 (+0s): 9.996µs self time
                                                    Gather#122 step 2/2 (+9.996µs): return nil
                                                    Gather#122 ends at 551.837µs
                                                Combine#124 step 5/6 (+6.436µs): 3.562µs self time
                                                Combine#124 step 6/6 (+9.998µs): return nil
                                                Combine#124 ends at 445.542µs
                                            Gather#125 step 3/4 (+5.025µs): 4.979µs self time
                                            Gather#125 step 4/4 (+10.004µs): return nil
                                            Gather#125 ends at 316.988µs
                                        Gather#127 step 3/4 (+4.769µs): 4.796µs self time
                                        Gather#127 step 4/4 (+9.565µs): return nil
                                        Gather#127 ends at 211.779µs
                                    Gather#135 step 3/4 (+2.329µs): 2.676µs self time
                                    Gather#135 step 4/4 (+5.005µs): return nil
                                    Gather#135 ends at 104.933µs
                                Plan#7 step 2/6 (+0s): scatter:
                                  Task#134: pool=0
                                  Task#134 step 1/2 (+0s): 99.995µs self time
                                  Task#134 step 2/2 (+99.995µs): return nil
                                  Task#134 ends at 99.995µs
                                    Gather#134: index=3
                                    Gather#134 step 1/4 (+0s): 5.002µs self time
                                    Gather#134 step 2/4 (+5.002µs): scatter:
                                      Task#117: pool=0
                                      Task#117 step 1/2 (+0s): 100.289µs self time
                                      Task#117 step 2/2 (+100.289µs): return nil
                                      Task#117 ends at 205.286µs
                                        Gather#117: index=2
                                        Gather#117 step 1/2 (+0s): 10.111µs self time
                                        Gather#117 step 2/2 (+10.111µs): return nil
                                        Gather#117 ends at 215.397µs
                                    Gather#134 step 3/4 (+5.002µs): 4.999µs self time
                                    Gather#134 step 4/4 (+10.001µs): return nil
                                    Gather#134 ends at 109.996µs
                                Plan#7 step 3/6 (+0s): scatter:
                                  Task#133: pool=0
                                  Task#133 step 1/2 (+0s): 99.986µs self time
                                  Task#133 step 2/2 (+99.986µs): return nil
                                  Task#133 ends at 99.986µs
                                    Gather#133: index=1
                                    Gather#133 step 1/4 (+0s): 7.954µs self time
                                    Gather#133 step 2/4 (+7.954µs): scatter:
                                      Task#128: pool=0
                                      Task#128 step 1/2 (+0s): 100.014µs self time
                                      Task#128 step 2/2 (+100.014µs): return nil
                                      Task#128 ends at 207.954µs
                                        Gather#128: index=1
                                        Gather#128 step 1/4 (+0s): 1.926128ms self time
                                        Gather#128 step 2/4 (+1.926128ms): scatter:
                                          Task#114: pool=0
                                          Task#114 step 1/2 (+0s): 84.108175ms self time
                                          Task#114 step 2/2 (+84.108175ms): return nil
                                          Task#114 ends at 86.242257ms
                                            Gather#114: index=0
                                            Gather#114 step 1/2 (+0s): 10.003µs self time
                                            Gather#114 step 2/2 (+10.003µs): return nil
                                            Gather#114 ends at 86.25226ms
                                        Gather#128 step 3/4 (+1.926128ms): 1.924293ms self time
                                        Gather#128 step 4/4 (+3.850421ms): return nil
                                        Gather#128 ends at 4.058375ms
                                    Gather#133 step 3/4 (+7.954µs): 2.044µs self time
                                    Gather#133 step 4/4 (+9.998µs): return nil
                                    Gather#133 ends at 109.984µs
                                Plan#7 step 4/6 (+0s): scatter:
                                  Task#131: pool=0
                                  Task#131 step 1/2 (+0s): 99.965µs self time
                                  Task#131 step 2/2 (+99.965µs): return nil
                                  Task#131 ends at 99.965µs
                                    Gather#131: index=1
                                    Gather#131 step 1/8 (+0s): 2.461µs self time
                                    Gather#131 step 2/8 (+2.461µs): scatter:
                                      Task#120: pool=0
                                      Task#120 step 1/2 (+0s): 99.983µs self time
                                      Task#120 step 2/2 (+99.983µs): return nil
                                      Task#120 ends at 202.409µs
                                        Gather#120: index=0
                                        Gather#120 step 1/2 (+0s): 14.645µs self time
                                        Gather#120 step 2/2 (+14.645µs): return nil
                                        Gather#120 ends at 217.054µs
                                    Gather#131 step 3/8 (+2.461µs): 2.38µs self time
                                    Gather#131 step 4/8 (+4.841µs): scatter:
                                      Task#129: pool=0
                                      Task#129 step 1/2 (+0s): 99.696µs self time
                                      Task#129 step 2/2 (+99.696µs): return nil
                                      Task#129 ends at 204.502µs
                                        Gather#129: index=0
                                        Gather#129 step 1/6 (+0s): 1.328µs self time
                                        Gather#129 step 2/6 (+1.328µs): scatter:
                                          Task#126: pool=0
                                          Task#126 step 1/2 (+0s): 99.992µs self time
                                          Task#126 step 2/2 (+99.992µs): return nil
                                          Task#126 ends at 305.822µs
                                            Gather#126: index=3
                                            Gather#126 step 1/4 (+0s): 5.038µs self time
                                            Gather#126 step 2/4 (+5.038µs): scatter:
                                              Task#116: pool=0
                                              Task#116 step 1/2 (+0s): 97.975µs self time
                                              Task#116 step 2/2 (+97.975µs): return nil
                                              Task#116 ends at 408.835µs
                                                Gather#116: index=2
                                                Gather#116 step 1/2 (+0s): 9.998µs self time
                                                Gather#116 step 2/2 (+9.998µs): return nil
                                                Gather#116 ends at 418.833µs
                                            Gather#126 step 3/4 (+5.038µs): 4.97µs self time
                                            Gather#126 step 4/4 (+10.008µs): return error
                                            Gather#126 ends at 315.83µs
                                        Gather#129 step 3/6 (+1.328µs): 8.672µs self time
                                        Gather#129 step 4/6 (+10µs): scatter:
                                          Task#119: pool=0
                                          Task#119 step 1/2 (+0s): 99.999µs self time
                                          Task#119 step 2/2 (+99.999µs): return nil
                                          Task#119 ends at 314.501µs
                                            Combine#119: index=1 flush=<nil>
                                            Combine#119 step 1/2 (+0s): 6.245µs self time
                                            Combine#119 step 2/2 (+6.245µs): return nil
                                            Combine#119 ends at 320.746µs
                                        Gather#129 step 5/6 (+10µs): 0s self time
                                        Gather#129 step 6/6 (+10µs): return nil
                                        Gather#129 ends at 214.502µs
                                    Gather#131 step 5/8 (+4.841µs): 3.257µs self time
                                    Gather#131 step 6/8 (+8.098µs): scatter:
                                      Task#118: pool=0
                                      Task#118 step 1/2 (+0s): 99.998µs self time
                                      Task#118 step 2/2 (+99.998µs): return nil
                                      Task#118 ends at 208.061µs
                                        Combine#118: index=1 flush=<nil>
                                        Combine#118 step 1/2 (+0s): 9.996µs self time
                                        Combine#118 step 2/2 (+9.996µs): return nil
                                        Combine#118 ends at 218.057µs
                                    Gather#131 step 7/8 (+8.098µs): 1.759µs self time
                                    Gather#131 step 8/8 (+9.857µs): return nil
                                    Gather#131 ends at 109.822µs
                                Plan#7 step 5/6 (+0s): scatter:
                                  Task#132: pool=0
                                  Task#132 step 1/2 (+0s): 99.868µs self time
                                  Task#132 step 2/2 (+99.868µs): return nil
                                  Task#132 ends at 99.868µs
                                    Gather#132: index=0
                                    Gather#132 step 1/4 (+0s): 5µs self time
                                    Gather#132 step 2/4 (+5µs): scatter:
                                      Task#130: pool=0
                                      Task#130 step 1/2 (+0s): 100ms self time
                                      Task#130 step 2/2 (+100ms): return nil
                                      Task#130 ends at 100.104868ms
                                        Combine#130: index=3 flush=<nil>
                                        Combine#130 step 1/4 (+0s): 1.691µs self time
                                        Combine#130 step 2/4 (+1.691µs): scatter:
                                          Task#115: pool=0
                                          Task#115 step 1/2 (+0s): 2.382913ms self time
                                          Task#115 step 2/2 (+2.382913ms): return nil
                                          Task#115 ends at 102.489472ms
                                            Gather#115: index=2
                                            Gather#115 step 1/2 (+0s): 9.998µs self time
                                            Gather#115 step 2/2 (+9.998µs): return nil
                                            Gather#115 ends at 102.49947ms
                                        Combine#130 step 3/4 (+1.691µs): 1.482µs self time
                                        Combine#130 step 4/4 (+3.173µs): return nil
                                        Combine#130 ends at 100.108041ms
                                    Gather#132 step 3/4 (+5µs): 4.998µs self time
                                    Gather#132 step 4/4 (+9.998µs): return nil
                                    Gather#132 ends at 109.866µs
                                Plan#7 step 6/6 (+0s): ends at 102.49947ms
                              Gather#113 step 3/6 (+102.50183ms): 4.897µs self time
                              Gather#113 step 4/6 (+102.506727ms): scatter:
                                Task#112: pool=1
                                Task#112 step 1/2 (+0s): 99.994µs self time
                                Task#112 step 2/2 (+99.994µs): return nil
                                Task#112 ends at 102.716714ms
                                  Gather#112: index=2
                                  Gather#112 step 1/4 (+0s): 3.805µs self time
                                  Gather#112 step 2/4 (+3.805µs): scatter:
                                    Task#45: pool=1
                                    Task#45 step 1/4 (+0s): 49.972µs self time
                                    Task#45 step 2/4 (+49.972µs): subjob:
                                      Plan#6: pathCount=19 taskCount=39 maxPathDuration=67.645902ms minGatherCount=36 maxGatherCount=52
                                         TaskPools[0]: TaskPool#27: limit=9
                                         TaskPools[1]: TaskPool#28: limit=3
                                         TaskPools[2]: TaskPool#29: limit=1
                                         TaskPools[3]: TaskPool#30: limit=3
                                         TaskPools[4]: TaskPool#31: limit=2
                                         TaskPools[5]: TaskPool#32: limit=6
                                         TaskPools[6]: TaskPool#33: limit=2
                                         CombinerPools[0]: CombinerPool#13: limit=8
                                         CombinerPools[1]: CombinerPool#14: limit=6
                                         CombinerPools[2]: CombinerPool#15: limit=1
                                         CombinerPools[3]: CombinerPool#16: limit=2
                                         CombinerPools[4]: CombinerPool#17: limit=1
                                         CombinerPools[5]: CombinerPool#18: limit=1
                                         CombinerPools[6]: CombinerPool#19: limit=4
                                         Combiners[0]: pool=0
                                         Combiners[1]: pool=6
                                         Combiners[2]: pool=2
                                      Plan#6 step 1/9 (+0s): scatter:
                                        Task#105: pool=2
                                        Task#105 step 1/2 (+0s): 99.685µs self time
                                        Task#105 step 2/2 (+99.685µs): return nil
                                        Task#105 ends at 99.685µs
                                          Combine#105: index=1 flush=<nil>
                                          Combine#105 step 1/4 (+0s): 4.23232ms self time
                                          Combine#105 step 2/4 (+4.23232ms): scatter:
                                            Task#101: pool=4
                                            Task#101 step 1/2 (+0s): 99.905µs self time
                                            Task#101 step 2/2 (+99.905µs): return nil
                                            Task#101 ends at 4.43191ms
                                              Gather#101: index=0
                                              Gather#101 step 1/10 (+0s): 8.026µs self time
                                              Gather#101 step 2/10 (+8.026µs): scatter:
                                                Task#90: pool=3
                                                Task#90 step 1/2 (+0s): 46.605µs self time
                                                Task#90 step 2/2 (+46.605µs): return nil
                                                Task#90 ends at 4.486541ms
                                                  Gather#90: index=6
                                                  Gather#90 step 1/2 (+0s): 9.307µs self time
                                                  Gather#90 step 2/2 (+9.307µs): return nil
                                                  Gather#90 ends at 4.495848ms
                                              Gather#101 step 3/10 (+8.026µs): 364ns self time
                                              Gather#101 step 4/10 (+8.39µs): scatter:
                                                Task#95: pool=0
                                                Task#95 step 1/2 (+0s): 99.583µs self time
                                                Task#95 step 2/2 (+99.583µs): return nil
                                                Task#95 ends at 4.539883ms
                                                  Gather#95: index=4
                                                  Gather#95 step 1/4 (+0s): 4.769µs self time
                                                  Gather#95 step 2/4 (+4.769µs): scatter:
                                                    Task#75: pool=2
                                                    Task#75 step 1/2 (+0s): 12.636417ms self time
                                                    Task#75 step 2/2 (+12.636417ms): return nil
                                                    Task#75 ends at 17.181069ms
                                                      Gather#75: index=1
                                                      Gather#75 step 1/2 (+0s): 9.979µs self time
                                                      Gather#75 step 2/2 (+9.979µs): return nil
                                                      Gather#75 ends at 17.191048ms
                                                  Gather#95 step 3/4 (+4.769µs): 5.232µs self time
                                                  Gather#95 step 4/4 (+10.001µs): return nil
                                                  Gather#95 ends at 4.549884ms
                                              Gather#101 step 5/10 (+8.39µs): 542ns self time
                                              Gather#101 step 6/10 (+8.932µs): scatter:
                                                Task#97: pool=0
                                                Task#97 step 1/2 (+0s): 107.889µs self time
                                                Task#97 step 2/2 (+107.889µs): return nil
                                                Task#97 ends at 4.548731ms
                                                  Gather#97: index=3
                                                  Gather#97 step 1/4 (+0s): 4.985µs self time
                                                  Gather#97 step 2/4 (+4.985µs): scatter:
                                                    Task#93: pool=6
                                                    Task#93 step 1/2 (+0s): 100.002µs self time
                                                    Task#93 step 2/2 (+100.002µs): return nil
                                                    Task#93 ends at 4.653718ms
                                                      Gather#93: index=1
                                                      Gather#93 step 1/4 (+0s): 4.981µs self time
                                                      Gather#93 step 2/4 (+4.981µs): scatter:
                                                        Task#81: pool=0
                                                        Task#81 step 1/2 (+0s): 94.725µs self time
                                                        Task#81 step 2/2 (+94.725µs): return nil
                                                        Task#81 ends at 4.753424ms
                                                          Gather#81: index=5
                                                          Gather#81 step 1/2 (+0s): 10.013µs self time
                                                          Gather#81 step 2/2 (+10.013µs): return nil
                                                          Gather#81 ends at 4.763437ms
                                                      Gather#93 step 3/4 (+4.981µs): 5.013µs self time
                                                      Gather#93 step 4/4 (+9.994µs): return nil
                                                      Gather#93 ends at 4.663712ms
                                                  Gather#97 step 3/4 (+4.985µs): 5.015µs self time
                                                  Gather#97 step 4/4 (+10µs): return nil
                                                  Gather#97 ends at 4.558731ms
                                              Gather#101 step 7/10 (+8.932µs): 550ns self time
                                              Gather#101 step 8/10 (+9.482µs): scatter:
                                                Task#98: pool=4
                                                Task#98 step 1/2 (+0s): 100.055µs self time
                                                Task#98 step 2/2 (+100.055µs): return nil
                                                Task#98 ends at 4.541447ms
                                                  Gather#98: index=8
                                                  Gather#98 step 1/4 (+0s): 4.845µs self time
                                                  Gather#98 step 2/4 (+4.845µs): scatter:
                                                    Task#94: pool=2
                                                    Task#94 step 1/2 (+0s): 41.275664ms self time
                                                    Task#94 step 2/2 (+41.275664ms): return nil
                                                    Task#94 ends at 45.821956ms
                                                      Gather#94: index=0
                                                      Gather#94 step 1/4 (+0s): 0s self time
                                                      Gather#94 step 2/4 (+0s): scatter:
                                                        Task#91: pool=0
                                                        Task#91 step 1/2 (+0s): 99.822µs self time
                                                        Task#91 step 2/2 (+99.822µs): return error
                                                        Task#91 ends at 45.921778ms
                                                          Combine#91: index=0 flush=<nil>
                                                          Combine#91 step 1/8 (+0s): 0s self time
                                                          Combine#91 step 2/8 (+0s): scatter:
                                                            Task#76: pool=1
                                                            Task#76 step 1/2 (+0s): 99.989µs self time
                                                            Task#76 step 2/2 (+99.989µs): return nil
                                                            Task#76 ends at 46.021767ms
                                                              Gather#76: index=7
                                                              Gather#76 step 1/2 (+0s): 10.002µs self time
                                                              Gather#76 step 2/2 (+10.002µs): return nil
                                                              Gather#76 ends at 46.031769ms
                                                          Combine#91 step 3/8 (+0s): 0s self time
                                                          Combine#91 step 4/8 (+0s): scatter:
                                                            Task#72: pool=1
                                                            Task#72 step 1/2 (+0s): 21.714138ms self time
                                                            Task#72 step 2/2 (+21.714138ms): return nil
                                                            Task#72 ends at 67.635916ms
                                                              Gather#72: index=3
                                                              Gather#72 step 1/2 (+0s): 9.986µs self time
                                                              Gather#72 step 2/2 (+9.986µs): return nil
                                                              Gather#72 ends at 67.645902ms
                                                          Combine#91 step 5/8 (+0s): 0s self time
                                                          Combine#91 step 6/8 (+0s): scatter:
                                                            Task#86: pool=4
                                                            Task#86 step 1/2 (+0s): 99.999µs self time
                                                            Task#86 step 2/2 (+99.999µs): return nil
                                                            Task#86 ends at 46.021777ms
                                                              Gather#86: index=1
                                                              Gather#86 step 1/2 (+0s): 10.009µs self time
                                                              Gather#86 step 2/2 (+10.009µs): return nil
                                                              Gather#86 ends at 46.031786ms
                                                          Combine#91 step 7/8 (+0s): 0s self time
                                                          Combine#91 step 8/8 (+0s): return nil
                                                          Combine#91 ends at 45.921778ms
                                                      Gather#94 step 3/4 (+0s): 13.891µs self time
                                                      Gather#94 step 4/4 (+13.891µs): return nil
                                                      Gather#94 ends at 45.835847ms
                                                  Gather#98 step 3/4 (+4.845µs): 5.156µs self time
                                                  Gather#98 step 4/4 (+10.001µs): return nil
                                                  Gather#98 ends at 4.551448ms
                                              Gather#101 step 9/10 (+9.482µs): 527ns self time
                                              Gather#101 step 10/10 (+10.009µs): return nil
                                              Gather#101 ends at 4.441919ms
                                          Combine#105 step 3/4 (+4.23232ms): 4.29128ms self time
                                          Combine#105 step 4/4 (+8.5236ms): return nil
                                          Combine#105 ends at 8.623285ms
                                      Plan#6 step 2/9 (+0s): scatter:
                                        Task#106: pool=1
                                        Task#106 step 1/2 (+0s): 0s self time
                                        Task#106 step 2/2 (+0s): return nil
                                        Task#106 ends at 0s
                                          Gather#106: index=0
                                          Gather#106 step 1/4 (+0s): 4.786µs self time
                                          Gather#106 step 2/4 (+4.786µs): scatter:
                                            Task#73: pool=6
                                            Task#73 step 1/2 (+0s): 99.989µs self time
                                            Task#73 step 2/2 (+99.989µs): return nil
                                            Task#73 ends at 104.775µs
                                              Gather#73: index=4
                                              Gather#73 step 1/2 (+0s): 3.343068ms self time
                                              Gather#73 step 2/2 (+3.343068ms): return nil
                                              Gather#73 ends at 3.447843ms
                                          Gather#106 step 3/4 (+4.786µs): 5.212µs self time
                                          Gather#106 step 4/4 (+9.998µs): return nil
                                          Gather#106 ends at 9.998µs
                                      Plan#6 step 3/9 (+0s): scatter:
                                        Task#103: pool=0
                                        Task#103 step 1/2 (+0s): 99.976µs self time
                                        Task#103 step 2/2 (+99.976µs): return nil
                                        Task#103 ends at 99.976µs
                                          Gather#103: index=0
                                          Gather#103 step 1/4 (+0s): 0s self time
                                          Gather#103 step 2/4 (+0s): scatter:
                                            Task#77: pool=5
                                            Task#77 step 1/2 (+0s): 99.863µs self time
                                            Task#77 step 2/2 (+99.863µs): return nil
                                            Task#77 ends at 199.839µs
                                              Gather#77: index=4
                                              Gather#77 step 1/2 (+0s): 150.491µs self time
                                              Gather#77 step 2/2 (+150.491µs): return nil
                                              Gather#77 ends at 350.33µs
                                          Gather#103 step 3/4 (+0s): 0s self time
                                          Gather#103 step 4/4 (+0s): return nil
                                          Gather#103 ends at 99.976µs
                                      Plan#6 step 4/9 (+0s): scatter:
                                        Task#104: pool=3
                                        Task#104 step 1/2 (+0s): 0s self time
                                        Task#104 step 2/2 (+0s): return nil
                                        Task#104 ends at 0s
                                          Gather#104: index=6
                                          Gather#104 step 1/4 (+0s): 4.6µs self time
                                          Gather#104 step 2/4 (+4.6µs): scatter:
                                            Task#102: pool=0
                                            Task#102 step 1/2 (+0s): 200.161µs self time
                                            Task#102 step 2/2 (+200.161µs): return nil
                                            Task#102 ends at 204.761µs
                                              Gather#102: index=6
                                              Gather#102 step 1/8 (+0s): 1.691µs self time
                                              Gather#102 step 2/8 (+1.691µs): scatter:
                                                Task#99: pool=0
                                                Task#99 step 1/2 (+0s): 99.999µs self time
                                                Task#99 step 2/2 (+99.999µs): return nil
                                                Task#99 ends at 306.451µs
                                                  Gather#99: index=1
                                                  Gather#99 step 1/4 (+0s): 6.834µs self time
                                                  Gather#99 step 2/4 (+6.834µs): scatter:
                                                    Task#85: pool=5
                                                    Task#85 step 1/2 (+0s): 100.419µs self time
                                                    Task#85 step 2/2 (+100.419µs): return nil
                                                    Task#85 ends at 413.704µs
                                                      Gather#85: index=3
                                                      Gather#85 step 1/2 (+0s): 10.003µs self time
                                                      Gather#85 step 2/2 (+10.003µs): return nil
                                                      Gather#85 ends at 423.707µs
                                                  Gather#99 step 3/4 (+6.834µs): 6.774µs self time
                                                  Gather#99 step 4/4 (+13.608µs): return nil
                                                  Gather#99 ends at 320.059µs
                                              Gather#102 step 3/8 (+1.691µs): 4.652µs self time
                                              Gather#102 step 4/8 (+6.343µs): scatter:
                                                Task#83: pool=5
                                                Task#83 step 1/2 (+0s): 27.146µs self time
                                                Task#83 step 2/2 (+27.146µs): return nil
                                                Task#83 ends at 238.25µs
                                                  Gather#83: index=6
                                                  Gather#83 step 1/2 (+0s): 9.977µs self time
                                                  Gather#83 step 2/2 (+9.977µs): return nil
                                                  Gather#83 ends at 248.227µs
                                              Gather#102 step 5/8 (+6.343µs): 1.832µs self time
                                              Gather#102 step 6/8 (+8.175µs): scatter:
                                                Task#84: pool=1
                                                Task#84 step 1/2 (+0s): 100.001µs self time
                                                Task#84 step 2/2 (+100.001µs): return nil
                                                Task#84 ends at 312.937µs
                                                  Gather#84: index=4
                                                  Gather#84 step 1/2 (+0s): 10.006µs self time
                                                  Gather#84 step 2/2 (+10.006µs): return nil
                                                  Gather#84 ends at 322.943µs
                                              Gather#102 step 7/8 (+8.175µs): 1.831µs self time
                                              Gather#102 step 8/8 (+10.006µs): return nil
                                              Gather#102 ends at 214.767µs
                                          Gather#104 step 3/4 (+4.6µs): 4.986µs self time
                                          Gather#104 step 4/4 (+9.586µs): return nil
                                          Gather#104 ends at 9.586µs
                                      Plan#6 step 5/9 (+0s): scatter:
                                        Task#110: pool=3
                                        Task#110 step 1/2 (+0s): 99.982µs self time
                                        Task#110 step 2/2 (+99.982µs): return nil
                                        Task#110 ends at 99.982µs
                                          Gather#110: index=3
                                          Gather#110 step 1/6 (+0s): 3.282µs self time
                                          Gather#110 step 2/6 (+3.282µs): scatter:
                                            Task#88: pool=1
                                            Task#88 step 1/2 (+0s): 100.008µs self time
                                            Task#88 step 2/2 (+100.008µs): return nil
                                            Task#88 ends at 203.272µs
                                              Gather#88: index=8
                                              Gather#88 step 1/2 (+0s): 10.203µs self time
                                              Gather#88 step 2/2 (+10.203µs): return nil
                                              Gather#88 ends at 213.475µs
                                          Gather#110 step 3/6 (+3.282µs): 6.537µs self time
                                          Gather#110 step 4/6 (+9.819µs): scatter:
                                            Task#82: pool=2
                                            Task#82 step 1/2 (+0s): 99.978µs self time
                                            Task#82 step 2/2 (+99.978µs): return nil
                                            Task#82 ends at 209.779µs
                                              Gather#82: index=8
                                              Gather#82 step 1/2 (+0s): 10.002µs self time
                                              Gather#82 step 2/2 (+10.002µs): return nil
                                              Gather#82 ends at 219.781µs
                                          Gather#110 step 5/6 (+9.819µs): 182ns self time
                                          Gather#110 step 6/6 (+10.001µs): return error
                                          Gather#110 ends at 109.983µs
                                      Plan#6 step 6/9 (+0s): scatter:
                                        Task#109: pool=6
                                        Task#109 step 1/2 (+0s): 81.428µs self time
                                        Task#109 step 2/2 (+81.428µs): return nil
                                        Task#109 ends at 81.428µs
                                          Combine#109: index=1 flush=<nil>
                                          Combine#109 step 1/4 (+0s): 5.47µs self time
                                          Combine#109 step 2/4 (+5.47µs): scatter:
                                            Task#89: pool=4
                                            Task#89 step 1/2 (+0s): 100.008µs self time
                                            Task#89 step 2/2 (+100.008µs): return error
                                            Task#89 ends at 186.906µs
                                              Gather#89: index=0
                                              Gather#89 step 1/2 (+0s): 10.002µs self time
                                              Gather#89 step 2/2 (+10.002µs): return nil
                                              Gather#89 ends at 196.908µs
                                          Combine#109 step 3/4 (+5.47µs): 4.529µs self time
                                          Combine#109 step 4/4 (+9.999µs): return nil
                                          Combine#109 ends at 91.427µs
                                      Plan#6 step 7/9 (+0s): scatter:
                                        Task#108: pool=1
                                        Task#108 step 1/2 (+0s): 13.616µs self time
                                        Task#108 step 2/2 (+13.616µs): return nil
                                        Task#108 ends at 13.616µs
                                          Gather#108: index=4
                                          Gather#108 step 1/4 (+0s): 2.921µs self time
                                          Gather#108 step 2/4 (+2.921µs): scatter:
                                            Task#80: pool=4
                                            Task#80 step 1/2 (+0s): 99.999µs self time
                                            Task#80 step 2/2 (+99.999µs): return nil
                                            Task#80 ends at 116.536µs
                                              Gather#80: index=7
                                              Gather#80 step 1/2 (+0s): 9.95µs self time
                                              Gather#80 step 2/2 (+9.95µs): return nil
                                              Gather#80 ends at 126.486µs
                                          Gather#108 step 3/4 (+2.921µs): 7.19µs self time
                                          Gather#108 step 4/4 (+10.111µs): return nil
                                          Gather#108 ends at 23.727µs
                                      Plan#6 step 8/9 (+0s): scatter:
                                        Task#107: pool=6
                                        Task#107 step 1/2 (+0s): 100.111µs self time
                                        Task#107 step 2/2 (+100.111µs): return nil
                                        Task#107 ends at 100.111µs
                                          Gather#107: index=4
                                          Gather#107 step 1/4 (+0s): 6.121µs self time
                                          Gather#107 step 2/4 (+6.121µs): scatter:
                                            Task#100: pool=2
                                            Task#100 step 1/2 (+0s): 99.697µs self time
                                            Task#100 step 2/2 (+99.697µs): return nil
                                            Task#100 ends at 205.929µs
                                              Gather#100: index=0
                                              Gather#100 step 1/6 (+0s): 2.69µs self time
                                              Gather#100 step 2/6 (+2.69µs): scatter:
                                                Task#96: pool=2
                                                Task#96 step 1/2 (+0s): 100.126µs self time
                                                Task#96 step 2/2 (+100.126µs): return nil
                                                Task#96 ends at 308.745µs
                                                  Gather#96: index=6
                                                  Gather#96 step 1/4 (+0s): 4.999µs self time
                                                  Gather#96 step 2/4 (+4.999µs): scatter:
                                                    Task#92: pool=2
                                                    Task#92 step 1/2 (+0s): 116.154µs self time
                                                    Task#92 step 2/2 (+116.154µs): return nil
                                                    Task#92 ends at 429.898µs
                                                      Gather#92: index=2
                                                      Gather#92 step 1/8 (+0s): 2.383µs self time
                                                      Gather#92 step 2/8 (+2.383µs): scatter:
                                                        Task#74: pool=2
                                                        Task#74 step 1/2 (+0s): 102.528µs self time
                                                        Task#74 step 2/2 (+102.528µs): return nil
                                                        Task#74 ends at 534.809µs
                                                          Gather#74: index=5
                                                          Gather#74 step 1/2 (+0s): 11.133µs self time
                                                          Gather#74 step 2/2 (+11.133µs): return nil
                                                          Gather#74 ends at 545.942µs
                                                      Gather#92 step 3/8 (+2.383µs): 2.499µs self time
                                                      Gather#92 step 4/8 (+4.882µs): scatter:
                                                        Task#78: pool=2
                                                        Task#78 step 1/2 (+0s): 99.997µs self time
                                                        Task#78 step 2/2 (+99.997µs): return nil
                                                        Task#78 ends at 534.777µs
                                                          Gather#78: index=3
                                                          Gather#78 step 1/2 (+0s): 7.203µs self time
                                                          Gather#78 step 2/2 (+7.203µs): return nil
                                                          Gather#78 ends at 541.98µs
                                                      Gather#92 step 5/8 (+4.882µs): 2.544µs self time
                                                      Gather#92 step 6/8 (+7.426µs): scatter:
                                                        Task#79: pool=3
                                                        Task#79 step 1/2 (+0s): 100.002µs self time
                                                        Task#79 step 2/2 (+100.002µs): return nil
                                                        Task#79 ends at 537.326µs
                                                          Gather#79: index=6
                                                          Gather#79 step 1/2 (+0s): 10µs self time
                                                          Gather#79 step 2/2 (+10µs): return nil
                                                          Gather#79 ends at 547.326µs
                                                      Gather#92 step 7/8 (+7.426µs): 2.572µs self time
                                                      Gather#92 step 8/8 (+9.998µs): return nil
                                                      Gather#92 ends at 439.896µs
                                                  Gather#96 step 3/4 (+4.999µs): 4.998µs self time
                                                  Gather#96 step 4/4 (+9.997µs): return nil
                                                  Gather#96 ends at 318.742µs
                                              Gather#100 step 3/6 (+2.69µs): 4.146µs self time
                                              Gather#100 step 4/6 (+6.836µs): scatter:
                                                Task#87: pool=3
                                                Task#87 step 1/2 (+0s): 100.007µs self time
                                                Task#87 step 2/2 (+100.007µs): return nil
                                                Task#87 ends at 312.772µs
                                                  Gather#87: index=4
                                                  Gather#87 step 1/2 (+0s): 10.007µs self time
                                                  Gather#87 step 2/2 (+10.007µs): return nil
                                                  Gather#87 ends at 322.779µs
                                              Gather#100 step 5/6 (+6.836µs): 4.178µs self time
                                              Gather#100 step 6/6 (+11.014µs): return nil
                                              Gather#100 ends at 216.943µs
                                          Gather#107 step 3/4 (+6.121µs): 3.356µs self time
                                          Gather#107 step 4/4 (+9.477µs): return nil
                                          Gather#107 ends at 109.588µs
                                      Plan#6 step 9/9 (+0s): ends at 67.645902ms
                                    Task#45 step 3/4 (+67.695874ms): 50.028µs self time
                                    Task#45 step 4/4 (+67.745902ms): return nil
                                    Task#45 ends at 170.466421ms
                                      Gather#45: index=0
                                      Gather#45 step 1/4 (+0s): 6.867µs self time
                                      Gather#45 step 2/4 (+6.867µs): subjob:
                                        Plan#5: pathCount=14 taskCount=26 maxPathDuration=2.58125ms minGatherCount=25 maxGatherCount=26
                                           TaskPools[0]: TaskPool#25: limit=1
                                           TaskPools[1]: TaskPool#26: limit=2
                                           CombinerPools[0]: CombinerPool#8: limit=1
                                           CombinerPools[1]: CombinerPool#9: limit=2
                                           CombinerPools[2]: CombinerPool#10: limit=1
                                           CombinerPools[3]: CombinerPool#11: limit=2
                                           CombinerPools[4]: CombinerPool#12: limit=6
                                           Combiners[0]: pool=2
                                           Combiners[1]: pool=3
                                           Combiners[2]: pool=0
                                           Combiners[3]: pool=2
                                        Plan#5 step 1/3 (+0s): scatter:
                                          Task#71: pool=0
                                          Task#71 step 1/2 (+0s): 99.986µs self time
                                          Task#71 step 2/2 (+99.986µs): return nil
                                          Task#71 ends at 99.986µs
                                            Gather#71: index=3
                                            Gather#71 step 1/6 (+0s): 3.331µs self time
                                            Gather#71 step 2/6 (+3.331µs): scatter:
                                              Task#50: pool=1
                                              Task#50 step 1/2 (+0s): 99.994µs self time
                                              Task#50 step 2/2 (+99.994µs): return nil
                                              Task#50 ends at 203.311µs
                                                Gather#50: index=1
                                                Gather#50 step 1/2 (+0s): 7.993µs self time
                                                Gather#50 step 2/2 (+7.993µs): return nil
                                                Gather#50 ends at 211.304µs
                                            Gather#71 step 3/6 (+3.331µs): 1.757µs self time
                                            Gather#71 step 4/6 (+5.088µs): scatter:
                                              Task#68: pool=1
                                              Task#68 step 1/2 (+0s): 100.598µs self time
                                              Task#68 step 2/2 (+100.598µs): return nil
                                              Task#68 ends at 205.672µs
                                                Gather#68: index=14
                                                Gather#68 step 1/4 (+0s): 5µs self time
                                                Gather#68 step 2/4 (+5µs): scatter:
                                                  Task#49: pool=0
                                                  Task#49 step 1/2 (+0s): 98.721µs self time
                                                  Task#49 step 2/2 (+98.721µs): return nil
                                                  Task#49 ends at 309.393µs
                                                    Gather#49: index=7
                                                    Gather#49 step 1/2 (+0s): 9.867µs self time
                                                    Gather#49 step 2/2 (+9.867µs): return nil
                                                    Gather#49 ends at 319.26µs
                                                Gather#68 step 3/4 (+5µs): 5.001µs self time
                                                Gather#68 step 4/4 (+10.001µs): return nil
                                                Gather#68 ends at 215.673µs
                                            Gather#71 step 5/6 (+5.088µs): 4.905µs self time
                                            Gather#71 step 6/6 (+9.993µs): return error
                                            Gather#71 ends at 109.979µs
                                        Plan#5 step 2/3 (+0s): scatter:
                                          Task#70: pool=0
                                          Task#70 step 1/2 (+0s): 99.985µs self time
                                          Task#70 step 2/2 (+99.985µs): return nil
                                          Task#70 ends at 99.985µs
                                            Gather#70: index=7
                                            Gather#70 step 1/12 (+0s): 1.951µs self time
                                            Gather#70 step 2/12 (+1.951µs): scatter:
                                              Task#52: pool=0
                                              Task#52 step 1/2 (+0s): 100.007µs self time
                                              Task#52 step 2/2 (+100.007µs): return nil
                                              Task#52 ends at 201.943µs
                                                Gather#52: index=6
                                                Gather#52 step 1/2 (+0s): 15.541µs self time
                                                Gather#52 step 2/2 (+15.541µs): return nil
                                                Gather#52 ends at 217.484µs
                                            Gather#70 step 3/12 (+1.951µs): 1.899µs self time
                                            Gather#70 step 4/12 (+3.85µs): scatter:
                                              Task#69: pool=1
                                              Task#69 step 1/2 (+0s): 27.078µs self time
                                              Task#69 step 2/2 (+27.078µs): return nil
                                              Task#69 ends at 130.913µs
                                                Gather#69: index=10
                                                Gather#69 step 1/8 (+0s): 10.037µs self time
                                                Gather#69 step 2/8 (+10.037µs): scatter:
                                                  Task#47: pool=1
                                                  Task#47 step 1/2 (+0s): 101.318µs self time
                                                  Task#47 step 2/2 (+101.318µs): return nil
                                                  Task#47 ends at 242.268µs
                                                    Combine#47: index=3 flush=<nil>
                                                    Combine#47 step 1/2 (+0s): 9.978µs self time
                                                    Combine#47 step 2/2 (+9.978µs): return nil
                                                    Combine#47 ends at 252.246µs
                                                Gather#69 step 3/8 (+10.037µs): 0s self time
                                                Gather#69 step 4/8 (+10.037µs): scatter:
                                                  Task#64: pool=1
                                                  Task#64 step 1/2 (+0s): 71.286µs self time
                                                  Task#64 step 2/2 (+71.286µs): return nil
                                                  Task#64 ends at 212.236µs
                                                    Gather#64: index=10
                                                    Gather#64 step 1/10 (+0s): 5.231µs self time
                                                    Gather#64 step 2/10 (+5.231µs): scatter:
                                                      Task#53: pool=1
                                                      Task#53 step 1/2 (+0s): 96.821µs self time
                                                      Task#53 step 2/2 (+96.821µs): return nil
                                                      Task#53 ends at 314.288µs
                                                        Gather#53: index=5
                                                        Gather#53 step 1/2 (+0s): 2.266962ms self time
                                                        Gather#53 step 2/2 (+2.266962ms): return nil
                                                        Gather#53 ends at 2.58125ms
                                                    Gather#64 step 3/10 (+5.231µs): 1.229µs self time
                                                    Gather#64 step 4/10 (+6.46µs): scatter:
                                                      Task#51: pool=0
                                                      Task#51 step 1/2 (+0s): 99.963µs self time
                                                      Task#51 step 2/2 (+99.963µs): return nil
                                                      Task#51 ends at 318.659µs
                                                        Gather#51: index=4
                                                        Gather#51 step 1/2 (+0s): 10.003µs self time
                                                        Gather#51 step 2/2 (+10.003µs): return nil
                                                        Gather#51 ends at 328.662µs
                                                    Gather#64 step 5/10 (+6.46µs): 382ns self time
                                                    Gather#64 step 6/10 (+6.842µs): scatter:
                                                      Task#48: pool=0
                                                      Task#48 step 1/2 (+0s): 99.999µs self time
                                                      Task#48 step 2/2 (+99.999µs): return nil
                                                      Task#48 ends at 319.077µs
                                                        Gather#48: index=4
                                                        Gather#48 step 1/2 (+0s): 8.684µs self time
                                                        Gather#48 step 2/2 (+8.684µs): return nil
                                                        Gather#48 ends at 327.761µs
                                                    Gather#64 step 7/10 (+6.842µs): 1.628µs self time
                                                    Gather#64 step 8/10 (+8.47µs): scatter:
                                                      Task#63: pool=0
                                                      Task#63 step 1/2 (+0s): 100.586µs self time
                                                      Task#63 step 2/2 (+100.586µs): return nil
                                                      Task#63 ends at 321.292µs
                                                        Gather#63: index=9
                                                        Gather#63 step 1/8 (+0s): 9.315µs self time
                                                        Gather#63 step 2/8 (+9.315µs): scatter:
                                                          Task#61: pool=0
                                                          Task#61 step 1/2 (+0s): 92.853µs self time
                                                          Task#61 step 2/2 (+92.853µs): return nil
                                                          Task#61 ends at 423.46µs
                                                            Gather#61: index=7
                                                            Gather#61 step 1/4 (+0s): 4.926µs self time
                                                            Gather#61 step 2/4 (+4.926µs): scatter:
                                                              Task#46: pool=0
                                                              Task#46 step 1/2 (+0s): 98.619µs self time
                                                              Task#46 step 2/2 (+98.619µs): return nil
                                                              Task#46 ends at 527.005µs
                                                                Gather#46: index=1
                                                                Gather#46 step 1/2 (+0s): 10.856µs self time
                                                                Gather#46 step 2/2 (+10.856µs): return nil
                                                                Gather#46 ends at 537.861µs
                                                            Gather#61 step 3/4 (+4.926µs): 3.192µs self time
                                                            Gather#61 step 4/4 (+8.118µs): return nil
                                                            Gather#61 ends at 431.578µs
                                                        Gather#63 step 3/8 (+9.315µs): 291ns self time
                                                        Gather#63 step 4/8 (+9.606µs): scatter:
                                                          Task#60: pool=1
                                                          Task#60 step 1/2 (+0s): 100.037µs self time
                                                          Task#60 step 2/2 (+100.037µs): return nil
                                                          Task#60 ends at 430.935µs
                                                            Gather#60: index=0
                                                            Gather#60 step 1/4 (+0s): 4.183µs self time
                                                            Gather#60 step 2/4 (+4.183µs): scatter:
                                                              Task#54: pool=0
                                                              Task#54 step 1/2 (+0s): 100.001µs self time
                                                              Task#54 step 2/2 (+100.001µs): return nil
                                                              Task#54 ends at 535.119µs
                                                                Gather#54: index=0
                                                                Gather#54 step 1/2 (+0s): 9.885µs self time
                                                                Gather#54 step 2/2 (+9.885µs): return nil
                                                                Gather#54 ends at 545.004µs
                                                            Gather#60 step 3/4 (+4.183µs): 4.2µs self time
                                                            Gather#60 step 4/4 (+8.383µs): return nil
                                                            Gather#60 ends at 439.318µs
                                                        Gather#63 step 5/8 (+9.606µs): 197ns self time
                                                        Gather#63 step 6/8 (+9.803µs): scatter:
                                                          Task#62: pool=1
                                                          Task#62 step 1/2 (+0s): 100.019µs self time
                                                          Task#62 step 2/2 (+100.019µs): return nil
                                                          Task#62 ends at 431.114µs
                                                            Gather#62: index=14
                                                            Gather#62 step 1/4 (+0s): 4.871µs self time
                                                            Gather#62 step 2/4 (+4.871µs): scatter:
                                                              Task#58: pool=1
                                                              Task#58 step 1/2 (+0s): 121.251µs self time
                                                              Task#58 step 2/2 (+121.251µs): return nil
                                                              Task#58 ends at 557.236µs
                                                                Gather#58: index=3
                                                                Gather#58 step 1/2 (+0s): 11.602µs self time
                                                                Gather#58 step 2/2 (+11.602µs): return nil
                                                                Gather#58 ends at 568.838µs
                                                            Gather#62 step 3/4 (+4.871µs): 4.873µs self time
                                                            Gather#62 step 4/4 (+9.744µs): return nil
                                                            Gather#62 ends at 440.858µs
                                                        Gather#63 step 7/8 (+9.803µs): 196ns self time
                                                        Gather#63 step 8/8 (+9.999µs): return nil
                                                        Gather#63 ends at 331.291µs
                                                    Gather#64 step 9/10 (+8.47µs): 1.631µs self time
                                                    Gather#64 step 10/10 (+10.101µs): return nil
                                                    Gather#64 ends at 222.337µs
                                                Gather#69 step 5/8 (+10.037µs): 0s self time
                                                Gather#69 step 6/8 (+10.037µs): scatter:
                                                  Task#65: pool=1
                                                  Task#65 step 1/2 (+0s): 100.001µs self time
                                                  Task#65 step 2/2 (+100.001µs): return nil
                                                  Task#65 ends at 240.951µs
                                                    Gather#65: index=1
                                                    Gather#65 step 1/4 (+0s): 6.067µs self time
                                                    Gather#65 step 2/4 (+6.067µs): scatter:
                                                      Task#57: pool=1
                                                      Task#57 step 1/2 (+0s): 99.997µs self time
                                                      Task#57 step 2/2 (+99.997µs): return nil
                                                      Task#57 ends at 347.015µs
                                                        Gather#57: index=0
                                                        Gather#57 step 1/2 (+0s): 10.002µs self time
                                                        Gather#57 step 2/2 (+10.002µs): return nil
                                                        Gather#57 ends at 357.017µs
                                                    Gather#65 step 3/4 (+6.067µs): 5.258µs self time
                                                    Gather#65 step 4/4 (+11.325µs): return nil
                                                    Gather#65 ends at 252.276µs
                                                Gather#69 step 7/8 (+10.037µs): 0s self time
                                                Gather#69 step 8/8 (+10.037µs): return nil
                                                Gather#69 ends at 140.95µs
                                            Gather#70 step 5/12 (+3.85µs): 1.911µs self time
                                            Gather#70 step 6/12 (+5.761µs): scatter:
                                              Task#67: pool=0
                                              Task#67 step 1/2 (+0s): 99.81µs self time
                                              Task#67 step 2/2 (+99.81µs): return nil
                                              Task#67 ends at 205.556µs
                                                Gather#67: index=6
                                                Gather#67 step 1/4 (+0s): 3.259µs self time
                                                Gather#67 step 2/4 (+3.259µs): scatter:
                                                  Task#56: pool=1
                                                  Task#56 step 1/2 (+0s): 99.997µs self time
                                                  Task#56 step 2/2 (+99.997µs): return nil
                                                  Task#56 ends at 308.812µs
                                                    Gather#56: index=14
                                                    Gather#56 step 1/2 (+0s): 10.06µs self time
                                                    Gather#56 step 2/2 (+10.06µs): return nil
                                                    Gather#56 ends at 318.872µs
                                                Gather#67 step 3/4 (+3.259µs): 4.245µs self time
                                                Gather#67 step 4/4 (+7.504µs): return nil
                                                Gather#67 ends at 213.06µs
                                            Gather#70 step 7/12 (+5.761µs): 926ns self time
                                            Gather#70 step 8/12 (+6.687µs): scatter:
                                              Task#66: pool=1
                                              Task#66 step 1/2 (+0s): 98.534µs self time
                                              Task#66 step 2/2 (+98.534µs): return nil
                                              Task#66 ends at 205.206µs
                                                Gather#66: index=8
                                                Gather#66 step 1/4 (+0s): 5.605µs self time
                                                Gather#66 step 2/4 (+5.605µs): scatter:
                                                  Task#59: pool=1
                                                  Task#59 step 1/2 (+0s): 99.768µs self time
                                                  Task#59 step 2/2 (+99.768µs): return nil
                                                  Task#59 ends at 310.579µs
                                                    Gather#59: index=2
                                                    Gather#59 step 1/2 (+0s): 813.663µs self time
                                                    Gather#59 step 2/2 (+813.663µs): return nil
                                                    Gather#59 ends at 1.124242ms
                                                Gather#66 step 3/4 (+5.605µs): 4.499µs self time
                                                Gather#66 step 4/4 (+10.104µs): return nil
                                                Gather#66 ends at 215.31µs
                                            Gather#70 step 9/12 (+6.687µs): 4.268µs self time
                                            Gather#70 step 10/12 (+10.955µs): scatter:
                                              Task#55: pool=0
                                              Task#55 step 1/2 (+0s): 70.312µs self time
                                              Task#55 step 2/2 (+70.312µs): return error
                                              Task#55 ends at 181.252µs
                                                Gather#55: index=3
                                                Gather#55 step 1/2 (+0s): 8.439µs self time
                                                Gather#55 step 2/2 (+8.439µs): return nil
                                                Gather#55 ends at 189.691µs
                                            Gather#70 step 11/12 (+10.955µs): 508ns self time
                                            Gather#70 step 12/12 (+11.463µs): return nil
                                            Gather#70 ends at 111.448µs
                                        Plan#5 step 3/3 (+0s): ends at 2.58125ms
                                      Gather#45 step 3/4 (+2.588117ms): 3.414µs self time
                                      Gather#45 step 4/4 (+2.591531ms): return nil
                                      Gather#45 ends at 173.057952ms
                                  Gather#112 step 3/4 (+3.805µs): 3.81µs self time
                                  Gather#112 step 4/4 (+7.615µs): return nil
                                  Gather#112 ends at 102.724329ms
                              Gather#113 step 5/6 (+102.506727ms): 0s self time
                              Gather#113 step 6/6 (+102.506727ms): return nil
                              Gather#113 ends at 102.61672ms
                          Gather#137 step 3/4 (+9.99µs): 0s self time
                          Gather#137 step 4/4 (+9.99µs): return nil
                          Gather#137 ends at 109.993µs
                      Plan#4 step 3/3 (+0s): ends at 173.057952ms
                    Task#44 step 3/4 (+173.105168ms): 52.759µs self time
                    Task#44 step 4/4 (+173.157927ms): return nil
                    Task#44 ends at 177.096868ms
                      Gather#44: index=2
                      Gather#44 step 1/4 (+0s): 5.001µs self time
                      Gather#44 step 2/4 (+5.001µs): scatter:
                        Task#42: pool=8
                        Task#42 step 1/2 (+0s): 7.682755ms self time
                        Task#42 step 2/2 (+7.682755ms): return nil
                        Task#42 ends at 184.784624ms
                          Gather#42: index=4
                          Gather#42 step 1/4 (+0s): 5.001µs self time
                          Gather#42 step 2/4 (+5.001µs): scatter:
                            Task#11: pool=2
                            Task#11 step 1/2 (+0s): 159.685µs self time
                            Task#11 step 2/2 (+159.685µs): return nil
                            Task#11 ends at 184.94931ms
                              Gather#11: index=2
                              Gather#11 step 1/6 (+0s): 3.495µs self time
                              Gather#11 step 2/6 (+3.495µs): scatter:
                                Task#10: pool=2
                                Task#10 step 1/2 (+0s): 100.061µs self time
                                Task#10 step 2/2 (+100.061µs): return nil
                                Task#10 ends at 185.052866ms
                                  Gather#10: index=1
                                  Gather#10 step 1/2 (+0s): 3.528445ms self time
                                  Gather#10 step 2/2 (+3.528445ms): return error
                                  Gather#10 ends at 188.581311ms
                              Gather#11 step 3/6 (+3.495µs): 152ns self time
                              Gather#11 step 4/6 (+3.647µs): subjob:
                                Plan#2: pathCount=1 taskCount=4 maxPathDuration=96.627242ms minGatherCount=4 maxGatherCount=4
                                   TaskPools[0]: TaskPool#12: limit=2
                                   CombinerPools[0]: CombinerPool#3: limit=2
                                   Combiners[0]: pool=0
                                   Combiners[1]: pool=0
                                   Combiners[2]: pool=0
                                   Combiners[3]: pool=0
                                Plan#2 step 1/2 (+0s): scatter:
                                  Task#40: pool=0
                                  Task#40 step 1/2 (+0s): 100.001µs self time
                                  Task#40 step 2/2 (+100.001µs): return nil
                                  Task#40 ends at 100.001µs
                                    Gather#40: index=6
                                    Gather#40 step 1/4 (+0s): 5.451µs self time
                                    Gather#40 step 2/4 (+5.451µs): scatter:
                                      Task#39: pool=0
                                      Task#39 step 1/2 (+0s): 122.35µs self time
                                      Task#39 step 2/2 (+122.35µs): return nil
                                      Task#39 ends at 227.802µs
                                        Gather#39: index=12
                                        Gather#39 step 1/4 (+0s): 5.126µs self time
                                        Gather#39 step 2/4 (+5.126µs): scatter:
                                          Task#13: pool=0
                                          Task#13 step 1/2 (+0s): 104.011µs self time
                                          Task#13 step 2/2 (+104.011µs): return nil
                                          Task#13 ends at 336.939µs
                                            Gather#13: index=13
                                            Gather#13 step 1/6 (+0s): 2.372µs self time
                                            Gather#13 step 2/6 (+2.372µs): subjob:
                                              Plan#3: pathCount=12 taskCount=25 maxPathDuration=96.186659ms minGatherCount=24 maxGatherCount=28
                                                 TaskPools[0]: TaskPool#13: limit=2
                                                 TaskPools[1]: TaskPool#14: limit=2
                                                 CombinerPools[0]: CombinerPool#4: limit=4
                                                 CombinerPools[1]: CombinerPool#5: limit=1
                                                 Combiners[0]: pool=1
                                                 Combiners[1]: pool=0
                                                 Combiners[2]: pool=0
                                                 Combiners[3]: pool=0
                                              Plan#3 step 1/5 (+0s): scatter:
                                                Task#36: pool=0
                                                Task#36 step 1/2 (+0s): 100.005µs self time
                                                Task#36 step 2/2 (+100.005µs): return nil
                                                Task#36 ends at 100.005µs
                                                  Gather#36: index=0
                                                  Gather#36 step 1/4 (+0s): 5.48µs self time
                                                  Gather#36 step 2/4 (+5.48µs): scatter:
                                                    Task#15: pool=1
                                                    Task#15 step 1/2 (+0s): 126.387µs self time
                                                    Task#15 step 2/2 (+126.387µs): return nil
                                                    Task#15 ends at 231.872µs
                                                      Gather#15: index=0
                                                      Gather#15 step 1/2 (+0s): 10.006µs self time
                                                      Gather#15 step 2/2 (+10.006µs): return nil
                                                      Gather#15 ends at 241.878µs
                                                  Gather#36 step 3/4 (+5.48µs): 4.522µs self time
                                                  Gather#36 step 4/4 (+10.002µs): return nil
                                                  Gather#36 ends at 110.007µs
                                              Plan#3 step 2/5 (+0s): scatter:
                                                Task#37: pool=1
                                                Task#37 step 1/2 (+0s): 99.974µs self time
                                                Task#37 step 2/2 (+99.974µs): return nil
                                                Task#37 ends at 99.974µs
                                                  Gather#37: index=1
                                                  Gather#37 step 1/4 (+0s): 5.143µs self time
                                                  Gather#37 step 2/4 (+5.143µs): scatter:
                                                    Task#24: pool=1
                                                    Task#24 step 1/2 (+0s): 96.071543ms self time
                                                    Task#24 step 2/2 (+96.071543ms): return nil
                                                    Task#24 ends at 96.17666ms
                                                      Gather#24: index=8
                                                      Gather#24 step 1/2 (+0s): 9.999µs self time
                                                      Gather#24 step 2/2 (+9.999µs): return nil
                                                      Gather#24 ends at 96.186659ms
                                                  Gather#37 step 3/4 (+5.143µs): 4.851µs self time
                                                  Gather#37 step 4/4 (+9.994µs): return nil
                                                  Gather#37 ends at 109.968µs
                                              Plan#3 step 3/5 (+0s): scatter:
                                                Task#35: pool=0
                                                Task#35 step 1/2 (+0s): 99.995µs self time
                                                Task#35 step 2/2 (+99.995µs): return nil
                                                Task#35 ends at 99.995µs
                                                  Gather#35: index=5
                                                  Gather#35 step 1/4 (+0s): 5.246µs self time
                                                  Gather#35 step 2/4 (+5.246µs): scatter:
                                                    Task#34: pool=0
                                                    Task#34 step 1/2 (+0s): 100.491µs self time
                                                    Task#34 step 2/2 (+100.491µs): return nil
                                                    Task#34 ends at 205.732µs
                                                      Gather#34: index=6
                                                      Gather#34 step 1/16 (+0s): 1.361µs self time
                                                      Gather#34 step 2/16 (+1.361µs): scatter:
                                                        Task#23: pool=1
                                                        Task#23 step 1/2 (+0s): 99.033µs self time
                                                        Task#23 step 2/2 (+99.033µs): return nil
                                                        Task#23 ends at 306.126µs
                                                          Gather#23: index=0
                                                          Gather#23 step 1/2 (+0s): 17.802µs self time
                                                          Gather#23 step 2/2 (+17.802µs): return nil
                                                          Gather#23 ends at 323.928µs
                                                      Gather#34 step 3/16 (+1.361µs): 8.622µs self time
                                                      Gather#34 step 4/16 (+9.983µs): scatter:
                                                        Task#29: pool=0
                                                        Task#29 step 1/2 (+0s): 99.988µs self time
                                                        Task#29 step 2/2 (+99.988µs): return nil
                                                        Task#29 ends at 315.703µs
                                                          Gather#29: index=2
                                                          Gather#29 step 1/4 (+0s): 683ns self time
                                                          Gather#29 step 2/4 (+683ns): scatter:
                                                            Task#27: pool=1
                                                            Task#27 step 1/2 (+0s): 37.929µs self time
                                                            Task#27 step 2/2 (+37.929µs): return nil
                                                            Task#27 ends at 354.315µs
                                                              Gather#27: index=0
                                                              Gather#27 step 1/6 (+0s): 3.328µs self time
                                                              Gather#27 step 2/6 (+3.328µs): scatter:
                                                                Task#26: pool=1
                                                                Task#26 step 1/2 (+0s): 98.123µs self time
                                                                Task#26 step 2/2 (+98.123µs): return nil
                                                                Task#26 ends at 455.766µs
                                                                  Gather#26: index=7
                                                                  Gather#26 step 1/4 (+0s): 872ns self time
                                                                  Gather#26 step 2/4 (+872ns): scatter:
                                                                    Task#21: pool=1
                                                                    Task#21 step 1/2 (+0s): 99.996µs self time
                                                                    Task#21 step 2/2 (+99.996µs): return nil
                                                                    Task#21 ends at 556.634µs
                                                                      Gather#21: index=8
                                                                      Gather#21 step 1/2 (+0s): 1.203595ms self time
                                                                      Gather#21 step 2/2 (+1.203595ms): return nil
                                                                      Gather#21 ends at 1.760229ms
                                                                  Gather#26 step 3/4 (+872ns): 9.15µs self time
                                                                  Gather#26 step 4/4 (+10.022µs): return error
                                                                  Gather#26 ends at 465.788µs
                                                              Gather#27 step 3/6 (+3.328µs): 3.361µs self time
                                                              Gather#27 step 4/6 (+6.689µs): scatter:
                                                                Task#17: pool=0
                                                                Task#17 step 1/2 (+0s): 100.225µs self time
                                                                Task#17 step 2/2 (+100.225µs): return nil
                                                                Task#17 ends at 461.229µs
                                                                  Gather#17: index=2
                                                                  Gather#17 step 1/2 (+0s): 218.017µs self time
                                                                  Gather#17 step 2/2 (+218.017µs): return nil
                                                                  Gather#17 ends at 679.246µs
                                                              Gather#27 step 5/6 (+6.689µs): 3.309µs self time
                                                              Gather#27 step 6/6 (+9.998µs): return nil
                                                              Gather#27 ends at 364.313µs
                                                          Gather#29 step 3/4 (+683ns): 732ns self time
                                                          Gather#29 step 4/4 (+1.415µs): return nil
                                                          Gather#29 ends at 317.118µs
                                                      Gather#34 step 5/16 (+9.983µs): 0s self time
                                                      Gather#34 step 6/16 (+9.983µs): scatter:
                                                        Task#16: pool=0
                                                        Task#16 step 1/2 (+0s): 100.002µs self time
                                                        Task#16 step 2/2 (+100.002µs): return nil
                                                        Task#16 ends at 315.717µs
                                                          Gather#16: index=8
                                                          Gather#16 step 1/2 (+0s): 9.987µs self time
                                                          Gather#16 step 2/2 (+9.987µs): return nil
                                                          Gather#16 ends at 325.704µs
                                                      Gather#34 step 7/16 (+9.983µs): 0s self time
                                                      Gather#34 step 8/16 (+9.983µs): scatter:
                                                        Task#31: pool=1
                                                        Task#31 step 1/2 (+0s): 169.561µs self time
                                                        Task#31 step 2/2 (+169.561µs): return nil
                                                        Task#31 ends at 385.276µs
                                                          Gather#31: index=0
                                                          Gather#31 step 1/4 (+0s): 5.008µs self time
                                                          Gather#31 step 2/4 (+5.008µs): scatter:
                                                            Task#22: pool=0
                                                            Task#22 step 1/2 (+0s): 99.944µs self time
                                                            Task#22 step 2/2 (+99.944µs): return nil
                                                            Task#22 ends at 490.228µs
                                                              Gather#22: index=3
                                                              Gather#22 step 1/2 (+0s): 10.005µs self time
                                                              Gather#22 step 2/2 (+10.005µs): return nil
                                                              Gather#22 ends at 500.233µs
                                                          Gather#31 step 3/4 (+5.008µs): 5.004µs self time
                                                          Gather#31 step 4/4 (+10.012µs): return nil
                                                          Gather#31 ends at 395.288µs
                                                      Gather#34 step 9/16 (+9.983µs): 0s self time
                                                      Gather#34 step 10/16 (+9.983µs): scatter:
                                                        Task#32: pool=1
                                                        Task#32 step 1/2 (+0s): 109.443µs self time
                                                        Task#32 step 2/2 (+109.443µs): return nil
                                                        Task#32 ends at 325.158µs
                                                          Gather#32: index=0
                                                          Gather#32 step 1/4 (+0s): 16.733µs self time
                                                          Gather#32 step 2/4 (+16.733µs): scatter:
                                                            Task#25: pool=1
                                                            Task#25 step 1/2 (+0s): 85.732µs self time
                                                            Task#25 step 2/2 (+85.732µs): return nil
                                                            Task#25 ends at 427.623µs
                                                              Combine#25: index=3 flush=<nil>
                                                              Combine#25 step 1/2 (+0s): 7.801µs self time
                                                              Combine#25 step 2/2 (+7.801µs): return nil
                                                              Combine#25 ends at 435.424µs
                                                          Gather#32 step 3/4 (+16.733µs): 16.735µs self time
                                                          Gather#32 step 4/4 (+33.468µs): return nil
                                                          Gather#32 ends at 358.626µs
                                                      Gather#34 step 11/16 (+9.983µs): 0s self time
                                                      Gather#34 step 12/16 (+9.983µs): scatter:
                                                        Task#20: pool=0
                                                        Task#20 step 1/2 (+0s): 99.983µs self time
                                                        Task#20 step 2/2 (+99.983µs): return nil
                                                        Task#20 ends at 315.698µs
                                                          Gather#20: index=0
                                                          Gather#20 step 1/2 (+0s): 2.678µs self time
                                                          Gather#20 step 2/2 (+2.678µs): return nil
                                                          Gather#20 ends at 318.376µs
                                                      Gather#34 step 13/16 (+9.983µs): 0s self time
                                                      Gather#34 step 14/16 (+9.983µs): scatter:
                                                        Task#19: pool=1
                                                        Task#19 step 1/2 (+0s): 26.331µs self time
                                                        Task#19 step 2/2 (+26.331µs): return nil
                                                        Task#19 ends at 242.046µs
                                                          Gather#19: index=1
                                                          Gather#19 step 1/2 (+0s): 9.993µs self time
                                                          Gather#19 step 2/2 (+9.993µs): return nil
                                                          Gather#19 ends at 252.039µs
                                                      Gather#34 step 15/16 (+9.983µs): 0s self time
                                                      Gather#34 step 16/16 (+9.983µs): return nil
                                                      Gather#34 ends at 215.715µs
                                                  Gather#35 step 3/4 (+5.246µs): 4.765µs self time
                                                  Gather#35 step 4/4 (+10.011µs): return nil
                                                  Gather#35 ends at 110.006µs
                                              Plan#3 step 4/5 (+0s): scatter:
                                                Task#38: pool=0
                                                Task#38 step 1/2 (+0s): 53.519µs self time
                                                Task#38 step 2/2 (+53.519µs): return nil
                                                Task#38 ends at 53.519µs
                                                  Gather#38: index=7
                                                  Gather#38 step 1/4 (+0s): 3.446µs self time
                                                  Gather#38 step 2/4 (+3.446µs): scatter:
                                                    Task#33: pool=1
                                                    Task#33 step 1/2 (+0s): 100.002µs self time
                                                    Task#33 step 2/2 (+100.002µs): return nil
                                                    Task#33 ends at 156.967µs
                                                      Gather#33: index=3
                                                      Gather#33 step 1/6 (+0s): 3.092µs self time
                                                      Gather#33 step 2/6 (+3.092µs): scatter:
                                                        Task#18: pool=1
                                                        Task#18 step 1/2 (+0s): 100.028µs self time
                                                        Task#18 step 2/2 (+100.028µs): return nil
                                                        Task#18 ends at 260.087µs
                                                          Gather#18: index=0
                                                          Gather#18 step 1/2 (+0s): 9.999µs self time
                                                          Gather#18 step 2/2 (+9.999µs): return nil
                                                          Gather#18 ends at 270.086µs
                                                      Gather#33 step 3/6 (+3.092µs): 2.443µs self time
                                                      Gather#33 step 4/6 (+5.535µs): scatter:
                                                        Task#30: pool=0
                                                        Task#30 step 1/2 (+0s): 99.842µs self time
                                                        Task#30 step 2/2 (+99.842µs): return nil
                                                        Task#30 ends at 262.344µs
                                                          Gather#30: index=3
                                                          Gather#30 step 1/4 (+0s): 4.998µs self time
                                                          Gather#30 step 2/4 (+4.998µs): scatter:
                                                            Task#28: pool=1
                                                            Task#28 step 1/2 (+0s): 100.001µs self time
                                                            Task#28 step 2/2 (+100.001µs): return nil
                                                            Task#28 ends at 367.343µs
                                                              Combine#28: index=3 flush=Gather#28
                                                              Combine#28 step 1/2 (+0s): 68.693µs self time
                                                              Combine#28 step 2/2 (+68.693µs): return nil
                                                              Combine#28 ends at 436.036µs
                                                                Gather#28: index=5
                                                                Gather#28 step 1/4 (+0s): 4.997µs self time
                                                                Gather#28 step 2/4 (+4.997µs): scatter:
                                                                  Task#14: pool=1
                                                                  Task#14 step 1/2 (+0s): 100.001µs self time
                                                                  Task#14 step 2/2 (+100.001µs): return nil
                                                                  Task#14 ends at 0s
                                                                    Gather#14: index=0
                                                                    Gather#14 step 1/2 (+0s): 10.004µs self time
                                                                    Gather#14 step 2/2 (+10.004µs): return nil
                                                                    Gather#14 ends at 0s
                                                                Gather#28 step 3/4 (+4.997µs): 4.995µs self time
                                                                Gather#28 step 4/4 (+9.992µs): return nil
                                                                Gather#28 ends at 0s
                                                          Gather#30 step 3/4 (+4.998µs): 4.999µs self time
                                                          Gather#30 step 4/4 (+9.997µs): return nil
                                                          Gather#30 ends at 272.341µs
                                                      Gather#33 step 5/6 (+5.535µs): 4.462µs self time
                                                      Gather#33 step 6/6 (+9.997µs): return nil
                                                      Gather#33 ends at 166.964µs
                                                  Gather#38 step 3/4 (+3.446µs): 6.558µs self time
                                                  Gather#38 step 4/4 (+10.004µs): return nil
                                                  Gather#38 ends at 63.523µs
                                              Plan#3 step 5/5 (+0s): ends at 96.186659ms
                                            Gather#13 step 3/6 (+96.189031ms): 3.82µs self time
                                            Gather#13 step 4/6 (+96.192851ms): scatter:
                                              Task#12: pool=0
                                              Task#12 step 1/2 (+0s): 97.452µs self time
                                              Task#12 step 2/2 (+97.452µs): return nil
                                              Task#12 ends at 96.627242ms
                                                Gather#12: index=1
                                                Gather#12 step 1/2 (+0s): 0s self time
                                                Gather#12 step 2/2 (+0s): return nil
                                                Gather#12 ends at 96.627242ms
                                            Gather#13 step 5/6 (+96.192851ms): 3.806µs self time
                                            Gather#13 step 6/6 (+96.196657ms): return nil
                                            Gather#13 ends at 96.533596ms
                                        Gather#39 step 3/4 (+5.126µs): 4.873µs self time
                                        Gather#39 step 4/4 (+9.999µs): return error
                                        Gather#39 ends at 237.801µs
                                    Gather#40 step 3/4 (+5.451µs): 4.549µs self time
                                    Gather#40 step 4/4 (+10µs): return nil
                                    Gather#40 ends at 110.001µs
                                Plan#2 step 2/2 (+0s): ends at 96.627242ms
                              Gather#11 step 5/6 (+96.630889ms): 6.844µs self time
                              Gather#11 step 6/6 (+96.637733ms): return nil
                              Gather#11 ends at 281.587043ms
                          Gather#42 step 3/4 (+5.001µs): 4.999µs self time
                          Gather#42 step 4/4 (+10µs): return nil
                          Gather#42 ends at 184.794624ms
                      Gather#44 step 3/4 (+5.001µs): 4.999µs self time
                      Gather#44 step 4/4 (+10µs): return nil
                      Gather#44 ends at 177.106868ms
                  Gather#142 step 3/4 (+5.093µs): 4.881µs self time
                  Gather#142 step 4/4 (+9.974µs): return nil
                  Gather#142 ends at 3.943822ms
              Gather#161 step 3/4 (+2.114µs): 4.017µs self time
              Gather#161 step 4/4 (+6.131µs): return nil
              Gather#161 ends at 106.125µs
          Plan#1 step 5/7 (+0s): scatter:
            Task#147: pool=6
            Task#147 step 1/2 (+0s): 100.001µs self time
            Task#147 step 2/2 (+100.001µs): return nil
            Task#147 ends at 100.001µs
              Gather#147: index=0
              Gather#147 step 1/8 (+0s): 2.774µs self time
              Gather#147 step 2/8 (+2.774µs): subjob:
                Plan#8: pathCount=6 taskCount=13 maxPathDuration=13.94255ms minGatherCount=11 maxGatherCount=19
                   TaskPools[0]: TaskPool#35: limit=1
                   TaskPools[1]: TaskPool#36: limit=9
                   CombinerPools[0]: CombinerPool#24: limit=9
                   CombinerPools[1]: CombinerPool#25: limit=4
                   CombinerPools[2]: CombinerPool#26: limit=2
                   CombinerPools[3]: CombinerPool#27: limit=7
                   CombinerPools[4]: CombinerPool#28: limit=1
                   CombinerPools[5]: CombinerPool#29: limit=4
                   CombinerPools[6]: CombinerPool#30: limit=4
                   Combiners[0]: pool=1
                Plan#8 step 1/2 (+0s): scatter:
                  Task#160: pool=0
                  Task#160 step 1/2 (+0s): 99.984µs self time
                  Task#160 step 2/2 (+99.984µs): return nil
                  Task#160 ends at 99.984µs
                    Combine#160: index=0 flush=Gather#160
                    Combine#160 step 1/6 (+0s): 3.439µs self time
                    Combine#160 step 2/6 (+3.439µs): scatter:
                      Task#159: pool=1
                      Task#159 step 1/2 (+0s): 116.069µs self time
                      Task#159 step 2/2 (+116.069µs): return nil
                      Task#159 ends at 219.492µs
                        Gather#159: index=13
                        Gather#159 step 1/6 (+0s): 3.363µs self time
                        Gather#159 step 2/6 (+3.363µs): scatter:
                          Task#156: pool=0
                          Task#156 step 1/2 (+0s): 28.373µs self time
                          Task#156 step 2/2 (+28.373µs): return nil
                          Task#156 ends at 251.228µs
                            Gather#156: index=2
                            Gather#156 step 1/8 (+0s): 3.907µs self time
                            Gather#156 step 2/8 (+3.907µs): scatter:
                              Task#149: pool=1
                              Task#149 step 1/2 (+0s): 99.974µs self time
                              Task#149 step 2/2 (+99.974µs): return nil
                              Task#149 ends at 355.109µs
                                Gather#149: index=12
                                Gather#149 step 1/2 (+0s): 10.276µs self time
                                Gather#149 step 2/2 (+10.276µs): return nil
                                Gather#149 ends at 365.385µs
                            Gather#156 step 3/8 (+3.907µs): 2.052µs self time
                            Gather#156 step 4/8 (+5.959µs): scatter:
                              Task#148: pool=1
                              Task#148 step 1/2 (+0s): 62.457µs self time
                              Task#148 step 2/2 (+62.457µs): return nil
                              Task#148 ends at 319.644µs
                                Gather#148: index=1
                                Gather#148 step 1/2 (+0s): 9.998µs self time
                                Gather#148 step 2/2 (+9.998µs): return nil
                                Gather#148 ends at 329.642µs
                            Gather#156 step 5/8 (+5.959µs): 1.092µs self time
                            Gather#156 step 6/8 (+7.051µs): scatter:
                              Task#152: pool=1
                              Task#152 step 1/2 (+0s): 99.999µs self time
                              Task#152 step 2/2 (+99.999µs): return nil
                              Task#152 ends at 358.278µs
                                Gather#152: index=2
                                Gather#152 step 1/2 (+0s): 5.708µs self time
                                Gather#152 step 2/2 (+5.708µs): return nil
                                Gather#152 ends at 363.986µs
                            Gather#156 step 7/8 (+7.051µs): 2.919µs self time
                            Gather#156 step 8/8 (+9.97µs): return nil
                            Gather#156 ends at 261.198µs
                        Gather#159 step 3/6 (+3.363µs): 1.066µs self time
                        Gather#159 step 4/6 (+4.429µs): scatter:
                          Task#153: pool=1
                          Task#153 step 1/2 (+0s): 99.827µs self time
                          Task#153 step 2/2 (+99.827µs): return nil
                          Task#153 ends at 323.748µs
                            Combine#153: index=0 flush=<nil>
                            Combine#153 step 1/2 (+0s): 10.005µs self time
                            Combine#153 step 2/2 (+10.005µs): return nil
                            Combine#153 ends at 333.753µs
                        Gather#159 step 5/6 (+4.429µs): 5.572µs self time
                        Gather#159 step 6/6 (+10.001µs): return nil
                        Gather#159 ends at 229.493µs
                    Combine#160 step 3/6 (+3.439µs): 5.992µs self time
                    Combine#160 step 4/6 (+9.431µs): scatter:
                      Task#158: pool=0
                      Task#158 step 1/2 (+0s): 55.574µs self time
                      Task#158 step 2/2 (+55.574µs): return nil
                      Task#158 ends at 164.989µs
                        Gather#158: index=2
                        Gather#158 step 1/4 (+0s): 5µs self time
                        Gather#158 step 2/4 (+5µs): scatter:
                          Task#157: pool=0
                          Task#157 step 1/2 (+0s): 100.006µs self time
                          Task#157 step 2/2 (+100.006µs): return nil
                          Task#157 ends at 269.995µs
                            Gather#157: index=0
                            Gather#157 step 1/4 (+0s): 5.083µs self time
                            Gather#157 step 2/4 (+5.083µs): scatter:
                              Task#155: pool=0
                              Task#155 step 1/2 (+0s): 100.907µs self time
                              Task#155 step 2/2 (+100.907µs): return nil
                              Task#155 ends at 375.985µs
                                Gather#155: index=2
                                Gather#155 step 1/6 (+0s): 2.548µs self time
                                Gather#155 step 2/6 (+2.548µs): scatter:
                                  Task#154: pool=1
                                  Task#154 step 1/2 (+0s): 99.989µs self time
                                  Task#154 step 2/2 (+99.989µs): return nil
                                  Task#154 ends at 478.522µs
                                    Gather#154: index=3
                                    Gather#154 step 1/4 (+0s): 5.028µs self time
                                    Gather#154 step 2/4 (+5.028µs): scatter:
                                      Task#150: pool=1
                                      Task#150 step 1/2 (+0s): 10.565µs self time
                                      Task#150 step 2/2 (+10.565µs): return nil
                                      Task#150 ends at 494.115µs
                                        Gather#150: index=1
                                        Gather#150 step 1/2 (+0s): 10µs self time
                                        Gather#150 step 2/2 (+10µs): return nil
                                        Gather#150 ends at 504.115µs
                                    Gather#154 step 3/4 (+5.028µs): 5.03µs self time
                                    Gather#154 step 4/4 (+10.058µs): return nil
                                    Gather#154 ends at 488.58µs
                                Gather#155 step 3/6 (+2.548µs): 3.729µs self time
                                Gather#155 step 4/6 (+6.277µs): scatter:
                                  Task#151: pool=0
                                  Task#151 step 1/2 (+0s): 13.550291ms self time
                                  Task#151 step 2/2 (+13.550291ms): return nil
                                  Task#151 ends at 13.932553ms
                                    Combine#151: index=0 flush=<nil>
                                    Combine#151 step 1/2 (+0s): 9.997µs self time
                                    Combine#151 step 2/2 (+9.997µs): return nil
                                    Combine#151 ends at 13.94255ms
                                Gather#155 step 5/6 (+6.277µs): 3.728µs self time
                                Gather#155 step 6/6 (+10.005µs): return nil
                                Gather#155 ends at 385.99µs
                            Gather#157 step 3/4 (+5.083µs): 4.974µs self time
                            Gather#157 step 4/4 (+10.057µs): return nil
                            Gather#157 ends at 280.052µs
                        Gather#158 step 3/4 (+5µs): 4.998µs self time
                        Gather#158 step 4/4 (+9.998µs): return nil
                        Gather#158 ends at 174.987µs
                    Combine#160 step 5/6 (+9.431µs): 531ns self time
                    Combine#160 step 6/6 (+9.962µs): return nil
                    Combine#160 ends at 109.946µs
                      Gather#160: index=1
                      Gather#160 step 1/2 (+0s): 25.437µs self time
                      Gather#160 step 2/2 (+25.437µs): return nil
                      Gather#160 ends at 0s
                Plan#8 step 2/2 (+0s): ends at 13.94255ms
              Gather#147 step 3/8 (+13.945324ms): 1.987µs self time
              Gather#147 step 4/8 (+13.947311ms): scatter:
                Task#141: pool=7
                Task#141 step 1/2 (+0s): 100.013µs self time
                Task#141 step 2/2 (+100.013µs): return nil
                Task#141 ends at 14.147325ms
                  Gather#141: index=1
                  Gather#141 step 1/4 (+0s): 3.549µs self time
                  Gather#141 step 2/4 (+3.549µs): scatter:
                    Task#139: pool=4
                    Task#139 step 1/2 (+0s): 100.005µs self time
                    Task#139 step 2/2 (+100.005µs): return error
                    Task#139 ends at 14.250879ms
                      Gather#139: index=3
                      Gather#139 step 1/4 (+0s): 5.899µs self time
                      Gather#139 step 2/4 (+5.899µs): scatter:
                        Task#43: pool=3
                        Task#43 step 1/2 (+0s): 97.103µs self time
                        Task#43 step 2/2 (+97.103µs): return nil
                        Task#43 ends at 14.353881ms
                          Gather#43: index=1
                          Gather#43 step 1/4 (+0s): 4.998µs self time
                          Gather#43 step 2/4 (+4.998µs): scatter:
                            Task#41: pool=8
                            Task#41 step 1/2 (+0s): 101.122µs self time
                            Task#41 step 2/2 (+101.122µs): return nil
                            Task#41 ends at 14.460001ms
                              Gather#41: index=4
                              Gather#41 step 1/4 (+0s): 4.997µs self time
                              Gather#41 step 2/4 (+4.997µs): scatter:
                                Task#3: pool=9
                                Task#3 step 1/2 (+0s): 27.679µs self time
                                Task#3 step 2/2 (+27.679µs): return nil
                                Task#3 ends at 14.492677ms
                                  Combine#3: index=0 flush=<nil>
                                  Combine#3 step 1/2 (+0s): 1.465µs self time
                                  Combine#3 step 2/2 (+1.465µs): return nil
                                  Combine#3 ends at 14.494142ms
                              Gather#41 step 3/4 (+4.997µs): 5.002µs self time
                              Gather#41 step 4/4 (+9.999µs): return nil
                              Gather#41 ends at 14.47ms
                          Gather#43 step 3/4 (+4.998µs): 5.001µs self time
                          Gather#43 step 4/4 (+9.999µs): return nil
                          Gather#43 ends at 14.36388ms
                      Gather#139 step 3/4 (+5.899µs): 5.894µs self time
                      Gather#139 step 4/4 (+11.793µs): return nil
                      Gather#139 ends at 14.262672ms
                  Gather#141 step 3/4 (+3.549µs): 6.455µs self time
                  Gather#141 step 4/4 (+10.004µs): return nil
                  Gather#141 ends at 14.157329ms
              Gather#147 step 5/8 (+13.947311ms): 2.096µs self time
              Gather#147 step 6/8 (+13.949407ms): scatter:
                Task#4: pool=6
                Task#4 step 1/2 (+0s): 100.002µs self time
                Task#4 step 2/2 (+100.002µs): return nil
                Task#4 ends at 14.14941ms
                  Gather#4: index=2
                  Gather#4 step 1/2 (+0s): 11.954µs self time
                  Gather#4 step 2/2 (+11.954µs): return nil
                  Gather#4 ends at 14.161364ms
              Gather#147 step 7/8 (+13.949407ms): 1.684µs self time
              Gather#147 step 8/8 (+13.951091ms): return nil
              Gather#147 ends at 14.051092ms
          Plan#1 step 6/7 (+0s): scatter:
            Task#179: pool=8
            Task#179 step 1/2 (+0s): 99.995µs self time
            Task#179 step 2/2 (+99.995µs): return nil
            Task#179 ends at 99.995µs
              Gather#179: index=1
              Gather#179 step 1/4 (+0s): 4.991µs self time
              Gather#179 step 2/4 (+4.991µs): scatter:
                Task#1: pool=1
                Task#1 step 1/2 (+0s): 99.758µs self time
                Task#1 step 2/2 (+99.758µs): return nil
                Task#1 ends at 204.744µs
                  Gather#1: index=3
                  Gather#1 step 1/2 (+0s): 9.987µs self time
                  Gather#1 step 2/2 (+9.987µs): return nil
                  Gather#1 ends at 214.731µs
              Gather#179 step 3/4 (+4.991µs): 5.006µs self time
              Gather#179 step 4/4 (+9.997µs): return nil
              Gather#179 ends at 109.992µs
          Plan#1 step 7/7 (+0s): ends at 281.587043ms
        Gather#0 step 3/4 (+281.592054ms): 4.989µs self time
        Gather#0 step 4/4 (+281.597043ms): return nil
        Gather#0 ends at 281.799482ms
    Gather#327 step 3/8 (+2.39µs): 2.331µs self time
    Gather#327 step 4/8 (+4.721µs): scatter:
      Task#181: pool=1
      Task#181 step 1/4 (+0s): 49.966µs self time
      Task#181 step 2/4 (+49.966µs): subjob:
        Plan#11: pathCount=6 taskCount=14 maxPathDuration=196.994999ms minGatherCount=13 maxGatherCount=19
           TaskPools[0]: TaskPool#41: limit=1
           TaskPools[1]: TaskPool#42: limit=8
           CombinerPools[0]: CombinerPool#36: limit=2
           CombinerPools[1]: CombinerPool#37: limit=8
           CombinerPools[2]: CombinerPool#38: limit=6
           Combiners[0]: pool=2
           Combiners[1]: pool=2
        Plan#11 step 1/4 (+0s): scatter:
          Task#239: pool=1
          Task#239 step 1/2 (+0s): 99.95µs self time
          Task#239 step 2/2 (+99.95µs): return nil
          Task#239 ends at 99.95µs
            Gather#239: index=1
            Gather#239 step 1/4 (+0s): 5.007µs self time
            Gather#239 step 2/4 (+5.007µs): scatter:
              Task#183: pool=1
              Task#183 step 1/2 (+0s): 99.627µs self time
              Task#183 step 2/2 (+99.627µs): return nil
              Task#183 ends at 204.584µs
                Gather#183: index=0
                Gather#183 step 1/2 (+0s): 9.997µs self time
                Gather#183 step 2/2 (+9.997µs): return nil
                Gather#183 ends at 214.581µs
            Gather#239 step 3/4 (+5.007µs): 4.991µs self time
            Gather#239 step 4/4 (+9.998µs): return nil
            Gather#239 ends at 109.948µs
        Plan#11 step 2/4 (+0s): scatter:
          Task#241: pool=1
          Task#241 step 1/4 (+0s): 36.943µs self time
          Task#241 step 2/4 (+36.943µs): subjob:
            Plan#15: pathCount=13 taskCount=26 maxPathDuration=196.877255ms minGatherCount=24 maxGatherCount=32
               TaskPools[0]: TaskPool#53: limit=7
               TaskPools[1]: TaskPool#54: limit=1
               TaskPools[2]: TaskPool#55: limit=10
               TaskPools[3]: TaskPool#56: limit=2
               TaskPools[4]: TaskPool#57: limit=4
               TaskPools[5]: TaskPool#58: limit=5
               CombinerPools[0]: CombinerPool#55: limit=4
               CombinerPools[1]: CombinerPool#56: limit=1
               Combiners[0]: pool=0
            Plan#15 step 1/4 (+0s): scatter:
              Task#297: pool=2
              Task#297 step 1/4 (+0s): 27.591µs self time
              Task#297 step 2/4 (+27.591µs): subjob:
                Plan#17: pathCount=13 taskCount=26 maxPathDuration=100.633809ms minGatherCount=21 maxGatherCount=45
                   TaskPools[0]: TaskPool#65: limit=4
                   CombinerPools[0]: CombinerPool#58: limit=10
                   CombinerPools[1]: CombinerPool#59: limit=1
                   CombinerPools[2]: CombinerPool#60: limit=1
                   CombinerPools[3]: CombinerPool#61: limit=2
                   Combiners[0]: pool=2
                   Combiners[1]: pool=0
                   Combiners[2]: pool=0
                   Combiners[3]: pool=1
                   Combiners[4]: pool=3
                   Combiners[5]: pool=0
                   Combiners[6]: pool=3
                   Combiners[7]: pool=0
                   Combiners[8]: pool=2
                   Combiners[9]: pool=3
                   Combiners[10]: pool=3
                   Combiners[11]: pool=0
                   Combiners[12]: pool=3
                   Combiners[13]: pool=2
                Plan#17 step 1/2 (+0s): scatter:
                  Task#323: pool=0
                  Task#323 step 1/2 (+0s): 90.392µs self time
                  Task#323 step 2/2 (+90.392µs): return nil
                  Task#323 ends at 90.392µs
                    Gather#323: index=1
                    Gather#323 step 1/18 (+0s): 219ns self time
                    Gather#323 step 2/18 (+219ns): scatter:
                      Task#319: pool=0
                      Task#319 step 1/2 (+0s): 39.862242ms self time
                      Task#319 step 2/2 (+39.862242ms): return nil
                      Task#319 ends at 39.952853ms
                        Gather#319: index=3
                        Gather#319 step 1/4 (+0s): 5.037µs self time
                        Gather#319 step 2/4 (+5.037µs): scatter:
                          Task#298: pool=0
                          Task#298 step 1/2 (+0s): 99.76µs self time
                          Task#298 step 2/2 (+99.76µs): return nil
                          Task#298 ends at 40.05765ms
                            Gather#298: index=2
                            Gather#298 step 1/2 (+0s): 10.013µs self time
                            Gather#298 step 2/2 (+10.013µs): return nil
                            Gather#298 ends at 40.067663ms
                        Gather#319 step 3/4 (+5.037µs): 4.977µs self time
                        Gather#319 step 4/4 (+10.014µs): return nil
                        Gather#319 ends at 39.962867ms
                    Gather#323 step 3/18 (+219ns): 1.315µs self time
                    Gather#323 step 4/18 (+1.534µs): scatter:
                      Task#310: pool=0
                      Task#310 step 1/2 (+0s): 100.949µs self time
                      Task#310 step 2/2 (+100.949µs): return nil
                      Task#310 ends at 192.875µs
                        Gather#310: index=1
                        Gather#310 step 1/2 (+0s): 10.96µs self time
                        Gather#310 step 2/2 (+10.96µs): return nil
                        Gather#310 ends at 203.835µs
                    Gather#323 step 5/18 (+1.534µs): 1.187µs self time
                    Gather#323 step 6/18 (+2.721µs): scatter:
                      Task#320: pool=0
                      Task#320 step 1/2 (+0s): 100.007µs self time
                      Task#320 step 2/2 (+100.007µs): return nil
                      Task#320 ends at 193.12µs
                        Combine#320: index=0 flush=<nil>
                        Combine#320 step 1/4 (+0s): 4.996µs self time
                        Combine#320 step 2/4 (+4.996µs): scatter:
                          Task#317: pool=0
                          Task#317 step 1/2 (+0s): 109.204µs self time
                          Task#317 step 2/2 (+109.204µs): return nil
                          Task#317 ends at 307.32µs
                            Gather#317: index=4
                            Gather#317 step 1/4 (+0s): 754ns self time
                            Gather#317 step 2/4 (+754ns): scatter:
                              Task#308: pool=0
                              Task#308 step 1/2 (+0s): 96.707µs self time
                              Task#308 step 2/2 (+96.707µs): return nil
                              Task#308 ends at 404.781µs
                                Gather#308: index=0
                                Gather#308 step 1/2 (+0s): 6.293µs self time
                                Gather#308 step 2/2 (+6.293µs): return nil
                                Gather#308 ends at 411.074µs
                            Gather#317 step 3/4 (+754ns): 7.872µs self time
                            Gather#317 step 4/4 (+8.626µs): return nil
                            Gather#317 ends at 315.946µs
                        Combine#320 step 3/4 (+4.996µs): 4.997µs self time
                        Combine#320 step 4/4 (+9.993µs): return nil
                        Combine#320 ends at 203.113µs
                    Gather#323 step 7/18 (+2.721µs): 1.214µs self time
                    Gather#323 step 8/18 (+3.935µs): scatter:
                      Task#301: pool=0
                      Task#301 step 1/2 (+0s): 99.997µs self time
                      Task#301 step 2/2 (+99.997µs): return nil
                      Task#301 ends at 194.324µs
                        Gather#301: index=2
                        Gather#301 step 1/2 (+0s): 9.998µs self time
                        Gather#301 step 2/2 (+9.998µs): return nil
                        Gather#301 ends at 204.322µs
                    Gather#323 step 9/18 (+3.935µs): 1.176µs self time
                    Gather#323 step 10/18 (+5.111µs): scatter:
                      Task#300: pool=0
                      Task#300 step 1/2 (+0s): 100.002µs self time
                      Task#300 step 2/2 (+100.002µs): return error
                      Task#300 ends at 195.505µs
                        Gather#300: index=3
                        Gather#300 step 1/2 (+0s): 10.001µs self time
                        Gather#300 step 2/2 (+10.001µs): return nil
                        Gather#300 ends at 205.506µs
                    Gather#323 step 11/18 (+5.111µs): 1.193µs self time
                    Gather#323 step 12/18 (+6.304µs): scatter:
                      Task#321: pool=0
                      Task#321 step 1/2 (+0s): 104.046µs self time
                      Task#321 step 2/2 (+104.046µs): return nil
                      Task#321 ends at 200.742µs
                        Gather#321: index=3
                        Gather#321 step 1/4 (+0s): 5.186µs self time
                        Gather#321 step 2/4 (+5.186µs): scatter:
                          Task#302: pool=0
                          Task#302 step 1/2 (+0s): 100.006µs self time
                          Task#302 step 2/2 (+100.006µs): return error
                          Task#302 ends at 305.934µs
                            Gather#302: index=1
                            Gather#302 step 1/2 (+0s): 10.078µs self time
                            Gather#302 step 2/2 (+10.078µs): return nil
                            Gather#302 ends at 316.012µs
                        Gather#321 step 3/4 (+5.186µs): 4.868µs self time
                        Gather#321 step 4/4 (+10.054µs): return nil
                        Gather#321 ends at 210.796µs
                    Gather#323 step 13/18 (+6.304µs): 683ns self time
                    Gather#323 step 14/18 (+6.987µs): scatter:
                      Task#322: pool=0
                      Task#322 step 1/2 (+0s): 552.21µs self time
                      Task#322 step 2/2 (+552.21µs): return nil
                      Task#322 ends at 649.589µs
                        Gather#322: index=3
                        Gather#322 step 1/4 (+0s): 5.003µs self time
                        Gather#322 step 2/4 (+5.003µs): scatter:
                          Task#314: pool=0
                          Task#314 step 1/2 (+0s): 100.002µs self time
                          Task#314 step 2/2 (+100.002µs): return nil
                          Task#314 ends at 754.594µs
                            Gather#314: index=2
                            Gather#314 step 1/4 (+0s): 5.001µs self time
                            Gather#314 step 2/4 (+5.001µs): scatter:
                              Task#313: pool=0
                              Task#313 step 1/2 (+0s): 99.974µs self time
                              Task#313 step 2/2 (+99.974µs): return nil
                              Task#313 ends at 859.569µs
                                Combine#313: index=1 flush=<nil>
                                Combine#313 step 1/6 (+0s): 4.611µs self time
                                Combine#313 step 2/6 (+4.611µs): scatter:
                                  Task#304: pool=0
                                  Task#304 step 1/2 (+0s): 0s self time
                                  Task#304 step 2/2 (+0s): return nil
                                  Task#304 ends at 864.18µs
                                    Gather#304: index=2
                                    Gather#304 step 1/2 (+0s): 5.862µs self time
                                    Gather#304 step 2/2 (+5.862µs): return nil
                                    Gather#304 ends at 870.042µs
                                Combine#313 step 3/6 (+4.611µs): 4.646µs self time
                                Combine#313 step 4/6 (+9.257µs): scatter:
                                  Task#311: pool=0
                                  Task#311 step 1/2 (+0s): 81.449µs self time
                                  Task#311 step 2/2 (+81.449µs): return nil
                                  Task#311 ends at 950.275µs
                                    Gather#311: index=0
                                    Gather#311 step 1/6 (+0s): 1.99µs self time
                                    Gather#311 step 2/6 (+1.99µs): scatter:
                                      Task#299: pool=0
                                      Task#299 step 1/2 (+0s): 99.668001ms self time
                                      Task#299 step 2/2 (+99.668001ms): return nil
                                      Task#299 ends at 100.620266ms
                                        Combine#299: index=1 flush=<nil>
                                        Combine#299 step 1/2 (+0s): 13.543µs self time
                                        Combine#299 step 2/2 (+13.543µs): return nil
                                        Combine#299 ends at 100.633809ms
                                    Gather#311 step 3/6 (+1.99µs): 2.446µs self time
                                    Gather#311 step 4/6 (+4.436µs): scatter:
                                      Task#306: pool=0
                                      Task#306 step 1/2 (+0s): 99.909µs self time
                                      Task#306 step 2/2 (+99.909µs): return nil
                                      Task#306 ends at 1.05462ms
                                        Combine#306: index=3 flush=<nil>
                                        Combine#306 step 1/2 (+0s): 10.674µs self time
                                        Combine#306 step 2/2 (+10.674µs): return nil
                                        Combine#306 ends at 1.065294ms
                                    Gather#311 step 5/6 (+4.436µs): 5.648µs self time
                                    Gather#311 step 6/6 (+10.084µs): return nil
                                    Gather#311 ends at 960.359µs
                                Combine#313 step 5/6 (+9.257µs): 4.569µs self time
                                Combine#313 step 6/6 (+13.826µs): return nil
                                Combine#313 ends at 873.395µs
                            Gather#314 step 3/4 (+5.001µs): 4.948µs self time
                            Gather#314 step 4/4 (+9.949µs): return nil
                            Gather#314 ends at 764.543µs
                        Gather#322 step 3/4 (+5.003µs): 5.001µs self time
                        Gather#322 step 4/4 (+10.004µs): return nil
                        Gather#322 ends at 659.593µs
                    Gather#323 step 15/18 (+6.987µs): 1.452µs self time
                    Gather#323 step 16/18 (+8.439µs): scatter:
                      Task#318: pool=0
                      Task#318 step 1/2 (+0s): 99.974µs self time
                      Task#318 step 2/2 (+99.974µs): return nil
                      Task#318 ends at 198.805µs
                        Gather#318: index=2
                        Gather#318 step 1/8 (+0s): 2.375µs self time
                        Gather#318 step 2/8 (+2.375µs): scatter:
                          Task#315: pool=0
                          Task#315 step 1/2 (+0s): 100.028µs self time
                          Task#315 step 2/2 (+100.028µs): return nil
                          Task#315 ends at 301.208µs
                            Gather#315: index=1
                            Gather#315 step 1/4 (+0s): 18.444µs self time
                            Gather#315 step 2/4 (+18.444µs): scatter:
                              Task#312: pool=0
                              Task#312 step 1/2 (+0s): 99.619µs self time
                              Task#312 step 2/2 (+99.619µs): return nil
                              Task#312 ends at 419.271µs
                                Combine#312: index=10 flush=<nil>
                                Combine#312 step 1/6 (+0s): 1.275µs self time
                                Combine#312 step 2/6 (+1.275µs): scatter:
                                  Task#303: pool=0
                                  Task#303 step 1/2 (+0s): 100.002µs self time
                                  Task#303 step 2/2 (+100.002µs): return nil
                                  Task#303 ends at 520.548µs
                                    Gather#303: index=2
                                    Gather#303 step 1/2 (+0s): 32.03µs self time
                                    Gather#303 step 2/2 (+32.03µs): return nil
                                    Gather#303 ends at 552.578µs
                                Combine#312 step 3/6 (+1.275µs): 4.298µs self time
                                Combine#312 step 4/6 (+5.573µs): scatter:
                                  Task#307: pool=0
                                  Task#307 step 1/2 (+0s): 100.003µs self time
                                  Task#307 step 2/2 (+100.003µs): return nil
                                  Task#307 ends at 524.847µs
                                    Gather#307: index=0
                                    Gather#307 step 1/2 (+0s): 9.998µs self time
                                    Gather#307 step 2/2 (+9.998µs): return nil
                                    Gather#307 ends at 534.845µs
                                Combine#312 step 5/6 (+5.573µs): 4.3µs self time
                                Combine#312 step 6/6 (+9.873µs): return nil
                                Combine#312 ends at 429.144µs
                            Gather#315 step 3/4 (+18.444µs): 18.442µs self time
                            Gather#315 step 4/4 (+36.886µs): return nil
                            Gather#315 ends at 338.094µs
                        Gather#318 step 3/8 (+2.375µs): 2.842µs self time
                        Gather#318 step 4/8 (+5.217µs): scatter:
                          Task#309: pool=0
                          Task#309 step 1/2 (+0s): 99.999µs self time
                          Task#309 step 2/2 (+99.999µs): return nil
                          Task#309 ends at 304.021µs
                            Gather#309: index=1
                            Gather#309 step 1/2 (+0s): 9.936µs self time
                            Gather#309 step 2/2 (+9.936µs): return error
                            Gather#309 ends at 313.957µs
                        Gather#318 step 5/8 (+5.217µs): 2.391µs self time
                        Gather#318 step 6/8 (+7.608µs): scatter:
                          Task#316: pool=0
                          Task#316 step 1/2 (+0s): 99.12µs self time
                          Task#316 step 2/2 (+99.12µs): return nil
                          Task#316 ends at 305.533µs
                            Gather#316: index=0
                            Gather#316 step 1/4 (+0s): 4.997µs self time
                            Gather#316 step 2/4 (+4.997µs): scatter:
                              Task#305: pool=0
                              Task#305 step 1/2 (+0s): 99.998µs self time
                              Task#305 step 2/2 (+99.998µs): return nil
                              Task#305 ends at 410.528µs
                                Gather#305: index=0
                                Gather#305 step 1/2 (+0s): 10.017µs self time
                                Gather#305 step 2/2 (+10.017µs): return nil
                                Gather#305 ends at 420.545µs
                            Gather#316 step 3/4 (+4.997µs): 5.006µs self time
                            Gather#316 step 4/4 (+10.003µs): return error
                            Gather#316 ends at 315.536µs
                        Gather#318 step 7/8 (+7.608µs): 2.395µs self time
                        Gather#318 step 8/8 (+10.003µs): return nil
                        Gather#318 ends at 208.808µs
                    Gather#323 step 17/18 (+8.439µs): 1.45µs self time
                    Gather#323 step 18/18 (+9.889µs): return nil
                    Gather#323 ends at 100.281µs
                Plan#17 step 2/2 (+0s): ends at 100.633809ms
              Task#297 step 3/4 (+100.6614ms): 72.429µs self time
              Task#297 step 4/4 (+100.733829ms): return nil
              Task#297 ends at 100.733829ms
                Gather#297: index=7
                Gather#297 step 1/6 (+0s): 3.336µs self time
                Gather#297 step 2/6 (+3.336µs): scatter:
                  Task#294: pool=1
                  Task#294 step 1/2 (+0s): 100.01µs self time
                  Task#294 step 2/2 (+100.01µs): return nil
                  Task#294 ends at 100.837175ms
                    Gather#294: index=7
                    Gather#294 step 1/16 (+0s): 1.355µs self time
                    Gather#294 step 2/16 (+1.355µs): scatter:
                      Task#290: pool=1
                      Task#290 step 1/2 (+0s): 99.998µs self time
                      Task#290 step 2/2 (+99.998µs): return nil
                      Task#290 ends at 100.938528ms
                        Gather#290: index=0
                        Gather#290 step 1/4 (+0s): 4.97µs self time
                        Gather#290 step 2/4 (+4.97µs): scatter:
                          Task#245: pool=1
                          Task#245 step 1/2 (+0s): 100.054µs self time
                          Task#245 step 2/2 (+100.054µs): return nil
                          Task#245 ends at 101.043552ms
                            Gather#245: index=7
                            Gather#245 step 1/2 (+0s): 9.968µs self time
                            Gather#245 step 2/2 (+9.968µs): return nil
                            Gather#245 ends at 101.05352ms
                        Gather#290 step 3/4 (+4.97µs): 4.969µs self time
                        Gather#290 step 4/4 (+9.939µs): return nil
                        Gather#290 ends at 100.948467ms
                    Gather#294 step 3/16 (+1.355µs): 4.838µs self time
                    Gather#294 step 4/16 (+6.193µs): scatter:
                      Task#288: pool=1
                      Task#288 step 1/2 (+0s): 99.871µs self time
                      Task#288 step 2/2 (+99.871µs): return nil
                      Task#288 ends at 100.943239ms
                        Gather#288: index=4
                        Gather#288 step 1/6 (+0s): 4.197µs self time
                        Gather#288 step 2/6 (+4.197µs): scatter:
                          Task#246: pool=5
                          Task#246 step 1/2 (+0s): 105.874µs self time
                          Task#246 step 2/2 (+105.874µs): return nil
                          Task#246 ends at 101.05331ms
                            Gather#246: index=3
                            Gather#246 step 1/2 (+0s): 7.744µs self time
                            Gather#246 step 2/2 (+7.744µs): return nil
                            Gather#246 ends at 101.061054ms
                        Gather#288 step 3/6 (+4.197µs): 2.902µs self time
                        Gather#288 step 4/6 (+7.099µs): scatter:
                          Task#250: pool=0
                          Task#250 step 1/2 (+0s): 99.99µs self time
                          Task#250 step 2/2 (+99.99µs): return nil
                          Task#250 ends at 101.050328ms
                            Gather#250: index=5
                            Gather#250 step 1/4 (+0s): 1.764µs self time
                            Gather#250 step 2/4 (+1.764µs): subjob:
                              Plan#16: pathCount=16 taskCount=30 maxPathDuration=95.823793ms minGatherCount=29 maxGatherCount=31
                                 TaskPools[0]: TaskPool#59: limit=1
                                 TaskPools[1]: TaskPool#60: limit=1
                                 TaskPools[2]: TaskPool#61: limit=1
                                 TaskPools[3]: TaskPool#62: limit=2
                                 TaskPools[4]: TaskPool#63: limit=3
                                 TaskPools[5]: TaskPool#64: limit=8
                                 CombinerPools[0]: CombinerPool#57: limit=2
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
                              Plan#16 step 1/6 (+0s): scatter:
                                Task#280: pool=4
                                Task#280 step 1/2 (+0s): 38.957µs self time
                                Task#280 step 2/2 (+38.957µs): return nil
                                Task#280 ends at 38.957µs
                                  Gather#280: index=16
                                  Gather#280 step 1/4 (+0s): 9.982µs self time
                                  Gather#280 step 2/4 (+9.982µs): scatter:
                                    Task#273: pool=3
                                    Task#273 step 1/2 (+0s): 100.109µs self time
                                    Task#273 step 2/2 (+100.109µs): return nil
                                    Task#273 ends at 149.048µs
                                      Combine#273: index=6 flush=Gather#273
                                      Combine#273 step 1/4 (+0s): 6.748µs self time
                                      Combine#273 step 2/4 (+6.748µs): scatter:
                                        Task#262: pool=5
                                        Task#262 step 1/2 (+0s): 100.047µs self time
                                        Task#262 step 2/2 (+100.047µs): return nil
                                        Task#262 ends at 255.843µs
                                          Gather#262: index=9
                                          Gather#262 step 1/2 (+0s): 9.944µs self time
                                          Gather#262 step 2/2 (+9.944µs): return nil
                                          Gather#262 ends at 265.787µs
                                      Combine#273 step 3/4 (+6.748µs): 3.329µs self time
                                      Combine#273 step 4/4 (+10.077µs): return nil
                                      Combine#273 ends at 159.125µs
                                        Gather#273: index=8
                                        Gather#273 step 1/2 (+0s): 9.993µs self time
                                        Gather#273 step 2/2 (+9.993µs): return nil
                                        Gather#273 ends at 0s
                                  Gather#280 step 3/4 (+9.982µs): 0s self time
                                  Gather#280 step 4/4 (+9.982µs): return nil
                                  Gather#280 ends at 48.939µs
                              Plan#16 step 2/6 (+0s): scatter:
                                Task#279: pool=2
                                Task#279 step 1/2 (+0s): 100µs self time
                                Task#279 step 2/2 (+100µs): return nil
                                Task#279 ends at 100µs
                                  Gather#279: index=0
                                  Gather#279 step 1/6 (+0s): 3.131µs self time
                                  Gather#279 step 2/6 (+3.131µs): scatter:
                                    Task#259: pool=1
                                    Task#259 step 1/2 (+0s): 102.66µs self time
                                    Task#259 step 2/2 (+102.66µs): return nil
                                    Task#259 ends at 205.791µs
                                      Gather#259: index=10
                                      Gather#259 step 1/2 (+0s): 15.232µs self time
                                      Gather#259 step 2/2 (+15.232µs): return nil
                                      Gather#259 ends at 221.023µs
                                  Gather#279 step 3/6 (+3.131µs): 3.62µs self time
                                  Gather#279 step 4/6 (+6.751µs): scatter:
                                    Task#253: pool=4
                                    Task#253 step 1/2 (+0s): 100.009µs self time
                                    Task#253 step 2/2 (+100.009µs): return nil
                                    Task#253 ends at 206.76µs
                                      Gather#253: index=14
                                      Gather#253 step 1/2 (+0s): 10.001µs self time
                                      Gather#253 step 2/2 (+10.001µs): return nil
                                      Gather#253 ends at 216.761µs
                                  Gather#279 step 5/6 (+6.751µs): 3.651µs self time
                                  Gather#279 step 6/6 (+10.402µs): return nil
                                  Gather#279 ends at 110.402µs
                              Plan#16 step 3/6 (+0s): scatter:
                                Task#278: pool=0
                                Task#278 step 1/2 (+0s): 99.998µs self time
                                Task#278 step 2/2 (+99.998µs): return nil
                                Task#278 ends at 99.998µs
                                  Gather#278: index=0
                                  Gather#278 step 1/4 (+0s): 6.91µs self time
                                  Gather#278 step 2/4 (+6.91µs): scatter:
                                    Task#254: pool=0
                                    Task#254 step 1/2 (+0s): 99.998µs self time
                                    Task#254 step 2/2 (+99.998µs): return nil
                                    Task#254 ends at 206.906µs
                                      Gather#254: index=10
                                      Gather#254 step 1/2 (+0s): 12.049µs self time
                                      Gather#254 step 2/2 (+12.049µs): return nil
                                      Gather#254 ends at 218.955µs
                                  Gather#278 step 3/4 (+6.91µs): 6.901µs self time
                                  Gather#278 step 4/4 (+13.811µs): return nil
                                  Gather#278 ends at 113.809µs
                              Plan#16 step 4/6 (+0s): scatter:
                                Task#277: pool=3
                                Task#277 step 1/2 (+0s): 516.818µs self time
                                Task#277 step 2/2 (+516.818µs): return nil
                                Task#277 ends at 516.818µs
                                  Gather#277: index=6
                                  Gather#277 step 1/4 (+0s): 5.031µs self time
                                  Gather#277 step 2/4 (+5.031µs): scatter:
                                    Task#274: pool=5
                                    Task#274 step 1/2 (+0s): 99.818µs self time
                                    Task#274 step 2/2 (+99.818µs): return nil
                                    Task#274 ends at 621.667µs
                                      Gather#274: index=0
                                      Gather#274 step 1/4 (+0s): 117ns self time
                                      Gather#274 step 2/4 (+117ns): scatter:
                                        Task#270: pool=1
                                        Task#270 step 1/2 (+0s): 100.329µs self time
                                        Task#270 step 2/2 (+100.329µs): return nil
                                        Task#270 ends at 722.113µs
                                          Gather#270: index=0
                                          Gather#270 step 1/4 (+0s): 5.035µs self time
                                          Gather#270 step 2/4 (+5.035µs): scatter:
                                            Task#261: pool=5
                                            Task#261 step 1/2 (+0s): 28.165µs self time
                                            Task#261 step 2/2 (+28.165µs): return nil
                                            Task#261 ends at 755.313µs
                                              Gather#261: index=15
                                              Gather#261 step 1/2 (+0s): 4.16688ms self time
                                              Gather#261 step 2/2 (+4.16688ms): return nil
                                              Gather#261 ends at 4.922193ms
                                          Gather#270 step 3/4 (+5.035µs): 4.952µs self time
                                          Gather#270 step 4/4 (+9.987µs): return nil
                                          Gather#270 ends at 732.1µs
                                      Gather#274 step 3/4 (+117ns): 9.882µs self time
                                      Gather#274 step 4/4 (+9.999µs): return nil
                                      Gather#274 ends at 631.666µs
                                  Gather#277 step 3/4 (+5.031µs): 4.976µs self time
                                  Gather#277 step 4/4 (+10.007µs): return nil
                                  Gather#277 ends at 526.825µs
                              Plan#16 step 5/6 (+0s): scatter:
                                Task#276: pool=0
                                Task#276 step 1/2 (+0s): 53.545µs self time
                                Task#276 step 2/2 (+53.545µs): return nil
                                Task#276 ends at 53.545µs
                                  Gather#276: index=2
                                  Gather#276 step 1/4 (+0s): 3.988µs self time
                                  Gather#276 step 2/4 (+3.988µs): scatter:
                                    Task#275: pool=1
                                    Task#275 step 1/2 (+0s): 100.012µs self time
                                    Task#275 step 2/2 (+100.012µs): return nil
                                    Task#275 ends at 157.545µs
                                      Gather#275: index=0
                                      Gather#275 step 1/10 (+0s): 6.01µs self time
                                      Gather#275 step 2/10 (+6.01µs): scatter:
                                        Task#252: pool=3
                                        Task#252 step 1/2 (+0s): 99.325µs self time
                                        Task#252 step 2/2 (+99.325µs): return nil
                                        Task#252 ends at 262.88µs
                                          Gather#252: index=6
                                          Gather#252 step 1/2 (+0s): 10.32µs self time
                                          Gather#252 step 2/2 (+10.32µs): return nil
                                          Gather#252 ends at 273.2µs
                                      Gather#275 step 3/10 (+6.01µs): 2.254µs self time
                                      Gather#275 step 4/10 (+8.264µs): scatter:
                                        Task#271: pool=4
                                        Task#271 step 1/2 (+0s): 100.475µs self time
                                        Task#271 step 2/2 (+100.475µs): return nil
                                        Task#271 ends at 266.284µs
                                          Gather#271: index=5
                                          Gather#271 step 1/4 (+0s): 4.949µs self time
                                          Gather#271 step 2/4 (+4.949µs): scatter:
                                            Task#255: pool=3
                                            Task#255 step 1/2 (+0s): 100.212µs self time
                                            Task#255 step 2/2 (+100.212µs): return nil
                                            Task#255 ends at 371.445µs
                                              Combine#255: index=10 flush=<nil>
                                              Combine#255 step 1/2 (+0s): 9.999µs self time
                                              Combine#255 step 2/2 (+9.999µs): return nil
                                              Combine#255 ends at 381.444µs
                                          Gather#271 step 3/4 (+4.949µs): 4.951µs self time
                                          Gather#271 step 4/4 (+9.9µs): return nil
                                          Gather#271 ends at 276.184µs
                                      Gather#275 step 5/10 (+8.264µs): 534ns self time
                                      Gather#275 step 6/10 (+8.798µs): scatter:
                                        Task#265: pool=5
                                        Task#265 step 1/2 (+0s): 100.012µs self time
                                        Task#265 step 2/2 (+100.012µs): return nil
                                        Task#265 ends at 266.355µs
                                          Gather#265: index=16
                                          Gather#265 step 1/2 (+0s): 9.998µs self time
                                          Gather#265 step 2/2 (+9.998µs): return nil
                                          Gather#265 ends at 276.353µs
                                      Gather#275 step 7/10 (+8.798µs): 796ns self time
                                      Gather#275 step 8/10 (+9.594µs): scatter:
                                        Task#272: pool=1
                                        Task#272 step 1/2 (+0s): 89.714058ms self time
                                        Task#272 step 2/2 (+89.714058ms): return nil
                                        Task#272 ends at 89.881197ms
                                          Gather#272: index=7
                                          Gather#272 step 1/4 (+0s): 4.999µs self time
                                          Gather#272 step 2/4 (+4.999µs): scatter:
                                            Task#269: pool=3
                                            Task#269 step 1/2 (+0s): 51.669µs self time
                                            Task#269 step 2/2 (+51.669µs): return nil
                                            Task#269 ends at 89.937865ms
                                              Gather#269: index=12
                                              Gather#269 step 1/12 (+0s): 7.749µs self time
                                              Gather#269 step 2/12 (+7.749µs): scatter:
                                                Task#258: pool=1
                                                Task#258 step 1/2 (+0s): 98.675µs self time
                                                Task#258 step 2/2 (+98.675µs): return nil
                                                Task#258 ends at 90.044289ms
                                                  Gather#258: index=1
                                                  Gather#258 step 1/2 (+0s): 9.278µs self time
                                                  Gather#258 step 2/2 (+9.278µs): return nil
                                                  Gather#258 ends at 90.053567ms
                                              Gather#269 step 3/12 (+7.749µs): 554ns self time
                                              Gather#269 step 4/12 (+8.303µs): scatter:
                                                Task#268: pool=3
                                                Task#268 step 1/2 (+0s): 100.012µs self time
                                                Task#268 step 2/2 (+100.012µs): return nil
                                                Task#268 ends at 90.04618ms
                                                  Gather#268: index=13
                                                  Gather#268 step 1/6 (+0s): 3.521µs self time
                                                  Gather#268 step 2/6 (+3.521µs): scatter:
                                                    Task#263: pool=0
                                                    Task#263 step 1/2 (+0s): 10.252µs self time
                                                    Task#263 step 2/2 (+10.252µs): return nil
                                                    Task#263 ends at 90.059953ms
                                                      Gather#263: index=15
                                                      Gather#263 step 1/2 (+0s): 9.999µs self time
                                                      Gather#263 step 2/2 (+9.999µs): return nil
                                                      Gather#263 ends at 90.069952ms
                                                  Gather#268 step 3/6 (+3.521µs): 2.802µs self time
                                                  Gather#268 step 4/6 (+6.323µs): scatter:
                                                    Task#251: pool=3
                                                    Task#251 step 1/2 (+0s): 99.993µs self time
                                                    Task#251 step 2/2 (+99.993µs): return nil
                                                    Task#251 ends at 90.152496ms
                                                      Gather#251: index=17
                                                      Gather#251 step 1/2 (+0s): 9.999µs self time
                                                      Gather#251 step 2/2 (+9.999µs): return nil
                                                      Gather#251 ends at 90.162495ms
                                                  Gather#268 step 5/6 (+6.323µs): 2.799µs self time
                                                  Gather#268 step 6/6 (+9.122µs): return nil
                                                  Gather#268 ends at 90.055302ms
                                              Gather#269 step 5/12 (+8.303µs): 418ns self time
                                              Gather#269 step 6/12 (+8.721µs): scatter:
                                                Task#267: pool=3
                                                Task#267 step 1/2 (+0s): 83.205µs self time
                                                Task#267 step 2/2 (+83.205µs): return nil
                                                Task#267 ends at 90.029791ms
                                                  Gather#267: index=14
                                                  Gather#267 step 1/8 (+0s): 46.849µs self time
                                                  Gather#267 step 2/8 (+46.849µs): scatter:
                                                    Task#256: pool=1
                                                    Task#256 step 1/2 (+0s): 5.578269ms self time
                                                    Task#256 step 2/2 (+5.578269ms): return nil
                                                    Task#256 ends at 95.654909ms
                                                      Gather#256: index=8
                                                      Gather#256 step 1/2 (+0s): 168.884µs self time
                                                      Gather#256 step 2/2 (+168.884µs): return nil
                                                      Gather#256 ends at 95.823793ms
                                                  Gather#267 step 3/8 (+46.849µs): 151.631µs self time
                                                  Gather#267 step 4/8 (+198.48µs): scatter:
                                                    Task#264: pool=5
                                                    Task#264 step 1/2 (+0s): 11.599µs self time
                                                    Task#264 step 2/2 (+11.599µs): return nil
                                                    Task#264 ends at 90.23987ms
                                                      Gather#264: index=14
                                                      Gather#264 step 1/2 (+0s): 5.065µs self time
                                                      Gather#264 step 2/2 (+5.065µs): return nil
                                                      Gather#264 ends at 90.244935ms
                                                  Gather#267 step 5/8 (+198.48µs): 152.362µs self time
                                                  Gather#267 step 6/8 (+350.842µs): scatter:
                                                    Task#266: pool=3
                                                    Task#266 step 1/2 (+0s): 133.267µs self time
                                                    Task#266 step 2/2 (+133.267µs): return nil
                                                    Task#266 ends at 90.5139ms
                                                      Gather#266: index=5
                                                      Gather#266 step 1/2 (+0s): 9.766µs self time
                                                      Gather#266 step 2/2 (+9.766µs): return nil
                                                      Gather#266 ends at 90.523666ms
                                                  Gather#267 step 7/8 (+350.842µs): 150.872µs self time
                                                  Gather#267 step 8/8 (+501.714µs): return nil
                                                  Gather#267 ends at 90.531505ms
                                              Gather#269 step 7/12 (+8.721µs): 299ns self time
                                              Gather#269 step 8/12 (+9.02µs): scatter:
                                                Task#260: pool=5
                                                Task#260 step 1/2 (+0s): 99.992µs self time
                                                Task#260 step 2/2 (+99.992µs): return nil
                                                Task#260 ends at 90.046877ms
                                                  Gather#260: index=9
                                                  Gather#260 step 1/2 (+0s): 10.005µs self time
                                                  Gather#260 step 2/2 (+10.005µs): return nil
                                                  Gather#260 ends at 90.056882ms
                                              Gather#269 step 9/12 (+9.02µs): 468ns self time
                                              Gather#269 step 10/12 (+9.488µs): scatter:
                                                Task#257: pool=1
                                                Task#257 step 1/2 (+0s): 100.003µs self time
                                                Task#257 step 2/2 (+100.003µs): return nil
                                                Task#257 ends at 90.047356ms
                                                  Gather#257: index=18
                                                  Gather#257 step 1/2 (+0s): 9.991µs self time
                                                  Gather#257 step 2/2 (+9.991µs): return nil
                                                  Gather#257 ends at 90.057347ms
                                              Gather#269 step 11/12 (+9.488µs): 508ns self time
                                              Gather#269 step 12/12 (+9.996µs): return nil
                                              Gather#269 ends at 89.947861ms
                                          Gather#272 step 3/4 (+4.999µs): 5µs self time
                                          Gather#272 step 4/4 (+9.999µs): return nil
                                          Gather#272 ends at 89.891196ms
                                      Gather#275 step 9/10 (+9.594µs): 297ns self time
                                      Gather#275 step 10/10 (+9.891µs): return error
                                      Gather#275 ends at 167.436µs
                                  Gather#276 step 3/4 (+3.988µs): 3.942µs self time
                                  Gather#276 step 4/4 (+7.93µs): return nil
                                  Gather#276 ends at 61.475µs
                              Plan#16 step 6/6 (+0s): ends at 95.823793ms
                            Gather#250 step 3/4 (+95.825557ms): 1.37µs self time
                            Gather#250 step 4/4 (+95.826927ms): return nil
                            Gather#250 ends at 196.877255ms
                        Gather#288 step 5/6 (+7.099µs): 2.912µs self time
                        Gather#288 step 6/6 (+10.011µs): return nil
                        Gather#288 ends at 100.95325ms
                    Gather#294 step 5/16 (+6.193µs): 465ns self time
                    Gather#294 step 6/16 (+6.658µs): scatter:
                      Task#242: pool=1
                      Task#242 step 1/2 (+0s): 99.993µs self time
                      Task#242 step 2/2 (+99.993µs): return nil
                      Task#242 ends at 100.943826ms
                        Gather#242: index=3
                        Gather#242 step 1/2 (+0s): 10.024µs self time
                        Gather#242 step 2/2 (+10.024µs): return nil
                        Gather#242 ends at 100.95385ms
                    Gather#294 step 7/16 (+6.658µs): 96ns self time
                    Gather#294 step 8/16 (+6.754µs): scatter:
                      Task#291: pool=0
                      Task#291 step 1/2 (+0s): 99.997µs self time
                      Task#291 step 2/2 (+99.997µs): return nil
                      Task#291 ends at 100.943926ms
                        Gather#291: index=7
                        Gather#291 step 1/4 (+0s): 4.941µs self time
                        Gather#291 step 2/4 (+4.941µs): scatter:
                          Task#286: pool=3
                          Task#286 step 1/2 (+0s): 100.299µs self time
                          Task#286 step 2/2 (+100.299µs): return nil
                          Task#286 ends at 101.049166ms
                            Gather#286: index=5
                            Gather#286 step 1/4 (+0s): 3.607µs self time
                            Gather#286 step 2/4 (+3.607µs): scatter:
                              Task#243: pool=1
                              Task#243 step 1/2 (+0s): 99.505µs self time
                              Task#243 step 2/2 (+99.505µs): return nil
                              Task#243 ends at 101.152278ms
                                Gather#243: index=3
                                Gather#243 step 1/2 (+0s): 942ns self time
                                Gather#243 step 2/2 (+942ns): return nil
                                Gather#243 ends at 101.15322ms
                            Gather#286 step 3/4 (+3.607µs): 6.39µs self time
                            Gather#286 step 4/4 (+9.997µs): return error
                            Gather#286 ends at 101.059163ms
                        Gather#291 step 3/4 (+4.941µs): 5.058µs self time
                        Gather#291 step 4/4 (+9.999µs): return nil
                        Gather#291 ends at 100.953925ms
                    Gather#294 step 9/16 (+6.754µs): 687ns self time
                    Gather#294 step 10/16 (+7.441µs): scatter:
                      Task#292: pool=5
                      Task#292 step 1/2 (+0s): 99.872µs self time
                      Task#292 step 2/2 (+99.872µs): return nil
                      Task#292 ends at 100.944488ms
                        Combine#292: index=0 flush=<nil>
                        Combine#292 step 1/4 (+0s): 3.924µs self time
                        Combine#292 step 2/4 (+3.924µs): scatter:
                          Task#248: pool=0
                          Task#248 step 1/2 (+0s): 99.66µs self time
                          Task#248 step 2/2 (+99.66µs): return nil
                          Task#248 ends at 101.048072ms
                            Gather#248: index=3
                            Gather#248 step 1/2 (+0s): 10ms self time
                            Gather#248 step 2/2 (+10ms): return nil
                            Gather#248 ends at 111.048072ms
                        Combine#292 step 3/4 (+3.924µs): 3.932µs self time
                        Combine#292 step 4/4 (+7.856µs): return nil
                        Combine#292 ends at 100.952344ms
                    Gather#294 step 11/16 (+7.441µs): 682ns self time
                    Gather#294 step 12/16 (+8.123µs): scatter:
                      Task#281: pool=3
                      Task#281 step 1/2 (+0s): 233.583µs self time
                      Task#281 step 2/2 (+233.583µs): return nil
                      Task#281 ends at 101.078881ms
                        Gather#281: index=7
                        Gather#281 step 1/2 (+0s): 9.751µs self time
                        Gather#281 step 2/2 (+9.751µs): return nil
                        Gather#281 ends at 101.088632ms
                    Gather#294 step 13/16 (+8.123µs): 320ns self time
                    Gather#294 step 14/16 (+8.443µs): scatter:
                      Task#247: pool=2
                      Task#247 step 1/2 (+0s): 100.011µs self time
                      Task#247 step 2/2 (+100.011µs): return nil
                      Task#247 ends at 100.945629ms
                        Gather#247: index=6
                        Gather#247 step 1/2 (+0s): 4.984153ms self time
                        Gather#247 step 2/2 (+4.984153ms): return nil
                        Gather#247 ends at 105.929782ms
                    Gather#294 step 15/16 (+8.443µs): 1.048µs self time
                    Gather#294 step 16/16 (+9.491µs): return nil
                    Gather#294 ends at 100.846666ms
                Gather#297 step 3/6 (+3.336µs): 3.343µs self time
                Gather#297 step 4/6 (+6.679µs): scatter:
                  Task#244: pool=5
                  Task#244 step 1/2 (+0s): 109.455µs self time
                  Task#244 step 2/2 (+109.455µs): return nil
                  Task#244 ends at 100.849963ms
                    Gather#244: index=0
                    Gather#244 step 1/2 (+0s): 9.996µs self time
                    Gather#244 step 2/2 (+9.996µs): return nil
                    Gather#244 ends at 100.859959ms
                Gather#297 step 5/6 (+6.679µs): 3.32µs self time
                Gather#297 step 6/6 (+9.999µs): return nil
                Gather#297 ends at 100.743828ms
            Plan#15 step 2/4 (+0s): scatter:
              Task#296: pool=1
              Task#296 step 1/2 (+0s): 99.712µs self time
              Task#296 step 2/2 (+99.712µs): return error
              Task#296 ends at 99.712µs
                Gather#296: index=0
                Gather#296 step 1/4 (+0s): 4.938µs self time
                Gather#296 step 2/4 (+4.938µs): scatter:
                  Task#283: pool=3
                  Task#283 step 1/2 (+0s): 99.986µs self time
                  Task#283 step 2/2 (+99.986µs): return nil
                  Task#283 ends at 204.636µs
                    Gather#283: index=0
                    Gather#283 step 1/2 (+0s): 9.996µs self time
                    Gather#283 step 2/2 (+9.996µs): return nil
                    Gather#283 ends at 214.632µs
                Gather#296 step 3/4 (+4.938µs): 5.021µs self time
                Gather#296 step 4/4 (+9.959µs): return nil
                Gather#296 ends at 109.671µs
            Plan#15 step 3/4 (+0s): scatter:
              Task#295: pool=4
              Task#295 step 1/2 (+0s): 65.336µs self time
              Task#295 step 2/2 (+65.336µs): return nil
              Task#295 ends at 65.336µs
                Gather#295: index=6
                Gather#295 step 1/4 (+0s): 4.792238ms self time
                Gather#295 step 2/4 (+4.792238ms): scatter:
                  Task#293: pool=4
                  Task#293 step 1/2 (+0s): 9.349µs self time
                  Task#293 step 2/2 (+9.349µs): return nil
                  Task#293 ends at 4.866923ms
                    Gather#293: index=3
                    Gather#293 step 1/4 (+0s): 4.217µs self time
                    Gather#293 step 2/4 (+4.217µs): scatter:
                      Task#289: pool=4
                      Task#289 step 1/2 (+0s): 99.658µs self time
                      Task#289 step 2/2 (+99.658µs): return nil
                      Task#289 ends at 4.970798ms
                        Gather#289: index=1
                        Gather#289 step 1/8 (+0s): 2.061µs self time
                        Gather#289 step 2/8 (+2.061µs): scatter:
                          Task#284: pool=4
                          Task#284 step 1/2 (+0s): 44.770036ms self time
                          Task#284 step 2/2 (+44.770036ms): return nil
                          Task#284 ends at 49.742895ms
                            Combine#284: index=0 flush=<nil>
                            Combine#284 step 1/2 (+0s): 9.97µs self time
                            Combine#284 step 2/2 (+9.97µs): return nil
                            Combine#284 ends at 49.752865ms
                        Gather#289 step 3/8 (+2.061µs): 2.64µs self time
                        Gather#289 step 4/8 (+4.701µs): scatter:
                          Task#249: pool=4
                          Task#249 step 1/2 (+0s): 99.972µs self time
                          Task#249 step 2/2 (+99.972µs): return nil
                          Task#249 ends at 5.075471ms
                            Gather#249: index=6
                            Gather#249 step 1/2 (+0s): 9.832µs self time
                            Gather#249 step 2/2 (+9.832µs): return nil
                            Gather#249 ends at 5.085303ms
                        Gather#289 step 5/8 (+4.701µs): 2.674µs self time
                        Gather#289 step 6/8 (+7.375µs): scatter:
                          Task#287: pool=5
                          Task#287 step 1/2 (+0s): 104.047µs self time
                          Task#287 step 2/2 (+104.047µs): return nil
                          Task#287 ends at 5.08222ms
                            Gather#287: index=0
                            Gather#287 step 1/4 (+0s): 1.704285ms self time
                            Gather#287 step 2/4 (+1.704285ms): scatter:
                              Task#285: pool=1
                              Task#285 step 1/2 (+0s): 100.015µs self time
                              Task#285 step 2/2 (+100.015µs): return nil
                              Task#285 ends at 6.88652ms
                                Gather#285: index=1
                                Gather#285 step 1/4 (+0s): 793.215µs self time
                                Gather#285 step 2/4 (+793.215µs): scatter:
                                  Task#282: pool=4
                                  Task#282 step 1/2 (+0s): 227.576µs self time
                                  Task#282 step 2/2 (+227.576µs): return error
                                  Task#282 ends at 7.907311ms
                                    Gather#282: index=2
                                    Gather#282 step 1/2 (+0s): 10.007µs self time
                                    Gather#282 step 2/2 (+10.007µs): return nil
                                    Gather#282 ends at 7.917318ms
                                Gather#285 step 3/4 (+793.215µs): 793.209µs self time
                                Gather#285 step 4/4 (+1.586424ms): return nil
                                Gather#285 ends at 8.472944ms
                            Gather#287 step 3/4 (+1.704285ms): 1.704509ms self time
                            Gather#287 step 4/4 (+3.408794ms): return nil
                            Gather#287 ends at 8.491014ms
                        Gather#289 step 7/8 (+7.375µs): 2.62µs self time
                        Gather#289 step 8/8 (+9.995µs): return nil
                        Gather#289 ends at 4.980793ms
                    Gather#293 step 3/4 (+4.217µs): 5.815µs self time
                    Gather#293 step 4/4 (+10.032µs): return nil
                    Gather#293 ends at 4.876955ms
                Gather#295 step 3/4 (+4.792238ms): 4.792241ms self time
                Gather#295 step 4/4 (+9.584479ms): return nil
                Gather#295 ends at 9.649815ms
            Plan#15 step 4/4 (+0s): ends at 196.877255ms
          Task#241 step 3/4 (+196.914198ms): 63.057µs self time
          Task#241 step 4/4 (+196.977255ms): return nil
          Task#241 ends at 196.977255ms
            Gather#241: index=1
            Gather#241 step 1/4 (+0s): 5.149µs self time
            Gather#241 step 2/4 (+5.149µs): scatter:
              Task#184: pool=1
              Task#184 step 1/2 (+0s): 0s self time
              Task#184 step 2/2 (+0s): return nil
              Task#184 ends at 196.982404ms
                Gather#184: index=2
                Gather#184 step 1/2 (+0s): 12.595µs self time
                Gather#184 step 2/2 (+12.595µs): return nil
                Gather#184 ends at 196.994999ms
            Gather#241 step 3/4 (+5.149µs): 4.848µs self time
            Gather#241 step 4/4 (+9.997µs): return nil
            Gather#241 ends at 196.987252ms
        Plan#11 step 3/4 (+0s): scatter:
          Task#240: pool=0
          Task#240 step 1/2 (+0s): 99.998µs self time
          Task#240 step 2/2 (+99.998µs): return nil
          Task#240 ends at 99.998µs
            Gather#240: index=12
            Gather#240 step 1/4 (+0s): 4.937µs self time
            Gather#240 step 2/4 (+4.937µs): scatter:
              Task#238: pool=1
              Task#238 step 1/2 (+0s): 59.593µs self time
              Task#238 step 2/2 (+59.593µs): return nil
              Task#238 ends at 164.528µs
                Combine#238: index=0 flush=<nil>
                Combine#238 step 1/6 (+0s): 3.222µs self time
                Combine#238 step 2/6 (+3.222µs): scatter:
                  Task#185: pool=0
                  Task#185 step 1/4 (+0s): 50.014µs self time
                  Task#185 step 2/4 (+50.014µs): subjob:
                    Plan#12: pathCount=4 taskCount=11 maxPathDuration=30.262648ms minGatherCount=11 maxGatherCount=11
                       TaskPools[0]: TaskPool#43: limit=6
                       CombinerPools[0]: CombinerPool#39: limit=2
                       CombinerPools[1]: CombinerPool#40: limit=10
                       CombinerPools[2]: CombinerPool#41: limit=7
                       CombinerPools[3]: CombinerPool#42: limit=2
                       CombinerPools[4]: CombinerPool#43: limit=1
                       CombinerPools[5]: CombinerPool#44: limit=1
                       CombinerPools[6]: CombinerPool#45: limit=10
                       CombinerPools[7]: CombinerPool#46: limit=2
                       CombinerPools[8]: CombinerPool#47: limit=3
                       Combiners[0]: pool=4
                       Combiners[1]: pool=3
                       Combiners[2]: pool=6
                       Combiners[3]: pool=7
                       Combiners[4]: pool=8
                       Combiners[5]: pool=1
                       Combiners[6]: pool=3
                       Combiners[7]: pool=1
                       Combiners[8]: pool=2
                       Combiners[9]: pool=8
                       Combiners[10]: pool=2
                    Plan#12 step 1/3 (+0s): scatter:
                      Task#219: pool=0
                      Task#219 step 1/2 (+0s): 100.001µs self time
                      Task#219 step 2/2 (+100.001µs): return nil
                      Task#219 ends at 100.001µs
                        Gather#219: index=4
                        Gather#219 step 1/4 (+0s): 4.998µs self time
                        Gather#219 step 2/4 (+4.998µs): scatter:
                          Task#187: pool=0
                          Task#187 step 1/2 (+0s): 99.979µs self time
                          Task#187 step 2/2 (+99.979µs): return nil
                          Task#187 ends at 204.978µs
                            Gather#187: index=3
                            Gather#187 step 1/2 (+0s): 10.203µs self time
                            Gather#187 step 2/2 (+10.203µs): return nil
                            Gather#187 ends at 215.181µs
                        Gather#219 step 3/4 (+4.998µs): 5.001µs self time
                        Gather#219 step 4/4 (+9.999µs): return nil
                        Gather#219 ends at 110µs
                    Plan#12 step 2/3 (+0s): scatter:
                      Task#195: pool=0
                      Task#195 step 1/2 (+0s): 100.081µs self time
                      Task#195 step 2/2 (+100.081µs): return nil
                      Task#195 ends at 100.081µs
                        Gather#195: index=12
                        Gather#195 step 1/8 (+0s): 5.277µs self time
                        Gather#195 step 2/8 (+5.277µs): subjob:
                          Plan#13: pathCount=13 taskCount=23 maxPathDuration=7.826039ms minGatherCount=21 maxGatherCount=34
                             TaskPools[0]: TaskPool#44: limit=10
                             CombinerPools[0]: CombinerPool#48: limit=4
                             CombinerPools[1]: CombinerPool#49: limit=10
                             CombinerPools[2]: CombinerPool#50: limit=2
                             CombinerPools[3]: CombinerPool#51: limit=1
                             CombinerPools[4]: CombinerPool#52: limit=3
                             Combiners[0]: pool=3
                             Combiners[1]: pool=2
                             Combiners[2]: pool=2
                             Combiners[3]: pool=0
                             Combiners[4]: pool=4
                             Combiners[5]: pool=0
                             Combiners[6]: pool=4
                             Combiners[7]: pool=1
                             Combiners[8]: pool=4
                             Combiners[9]: pool=0
                             Combiners[10]: pool=4
                             Combiners[11]: pool=2
                             Combiners[12]: pool=1
                             Combiners[13]: pool=2
                             Combiners[14]: pool=1
                             Combiners[15]: pool=0
                             Combiners[16]: pool=2
                             Combiners[17]: pool=0
                          Plan#13 step 1/4 (+0s): scatter:
                            Task#217: pool=0
                            Task#217 step 1/2 (+0s): 100.004µs self time
                            Task#217 step 2/2 (+100.004µs): return nil
                            Task#217 ends at 100.004µs
                              Gather#217: index=1
                              Gather#217 step 1/4 (+0s): 4.212µs self time
                              Gather#217 step 2/4 (+4.212µs): scatter:
                                Task#213: pool=0
                                Task#213 step 1/2 (+0s): 99.998µs self time
                                Task#213 step 2/2 (+99.998µs): return nil
                                Task#213 ends at 204.214µs
                                  Gather#213: index=3
                                  Gather#213 step 1/12 (+0s): 8.508µs self time
                                  Gather#213 step 2/12 (+8.508µs): scatter:
                                    Task#197: pool=0
                                    Task#197 step 1/2 (+0s): 97.402µs self time
                                    Task#197 step 2/2 (+97.402µs): return nil
                                    Task#197 ends at 310.124µs
                                      Gather#197: index=6
                                      Gather#197 step 1/2 (+0s): 11.832µs self time
                                      Gather#197 step 2/2 (+11.832µs): return nil
                                      Gather#197 ends at 321.956µs
                                  Gather#213 step 3/12 (+8.508µs): 200ns self time
                                  Gather#213 step 4/12 (+8.708µs): scatter:
                                    Task#200: pool=0
                                    Task#200 step 1/2 (+0s): 99.999µs self time
                                    Task#200 step 2/2 (+99.999µs): return nil
                                    Task#200 ends at 312.921µs
                                      Gather#200: index=1
                                      Gather#200 step 1/2 (+0s): 6.154µs self time
                                      Gather#200 step 2/2 (+6.154µs): return nil
                                      Gather#200 ends at 319.075µs
                                  Gather#213 step 5/12 (+8.708µs): 222ns self time
                                  Gather#213 step 6/12 (+8.93µs): scatter:
                                    Task#212: pool=0
                                    Task#212 step 1/2 (+0s): 0s self time
                                    Task#212 step 2/2 (+0s): return nil
                                    Task#212 ends at 213.144µs
                                      Gather#212: index=1
                                      Gather#212 step 1/10 (+0s): 2.002µs self time
                                      Gather#212 step 2/10 (+2.002µs): scatter:
                                        Task#202: pool=0
                                        Task#202 step 1/2 (+0s): 100.001µs self time
                                        Task#202 step 2/2 (+100.001µs): return nil
                                        Task#202 ends at 315.147µs
                                          Gather#202: index=5
                                          Gather#202 step 1/2 (+0s): 10.024µs self time
                                          Gather#202 step 2/2 (+10.024µs): return nil
                                          Gather#202 ends at 325.171µs
                                      Gather#212 step 3/10 (+2.002µs): 1.998µs self time
                                      Gather#212 step 4/10 (+4µs): scatter:
                                        Task#209: pool=0
                                        Task#209 step 1/2 (+0s): 100.087µs self time
                                        Task#209 step 2/2 (+100.087µs): return nil
                                        Task#209 ends at 317.231µs
                                          Gather#209: index=5
                                          Gather#209 step 1/6 (+0s): 6.408908ms self time
                                          Gather#209 step 2/6 (+6.408908ms): scatter:
                                            Task#207: pool=0
                                            Task#207 step 1/2 (+0s): 99.999µs self time
                                            Task#207 step 2/2 (+99.999µs): return error
                                            Task#207 ends at 6.826138ms
                                              Combine#207: index=12 flush=<nil>
                                              Combine#207 step 1/2 (+0s): 9.998µs self time
                                              Combine#207 step 2/2 (+9.998µs): return nil
                                              Combine#207 ends at 6.836136ms
                                          Gather#209 step 3/6 (+6.408908ms): 549.952µs self time
                                          Gather#209 step 4/6 (+6.95886ms): scatter:
                                            Task#206: pool=0
                                            Task#206 step 1/2 (+0s): 77.655µs self time
                                            Task#206 step 2/2 (+77.655µs): return nil
                                            Task#206 ends at 7.353746ms
                                              Gather#206: index=3
                                              Gather#206 step 1/2 (+0s): 40.331µs self time
                                              Gather#206 step 2/2 (+40.331µs): return nil
                                              Gather#206 ends at 7.394077ms
                                          Gather#209 step 5/6 (+6.95886ms): 549.948µs self time
                                          Gather#209 step 6/6 (+7.508808ms): return nil
                                          Gather#209 ends at 7.826039ms
                                      Gather#212 step 5/10 (+4µs): 1.096µs self time
                                      Gather#212 step 6/10 (+5.096µs): scatter:
                                        Task#208: pool=0
                                        Task#208 step 1/2 (+0s): 100.079µs self time
                                        Task#208 step 2/2 (+100.079µs): return nil
                                        Task#208 ends at 318.319µs
                                          Gather#208: index=0
                                          Gather#208 step 1/2 (+0s): 9.997µs self time
                                          Gather#208 step 2/2 (+9.997µs): return nil
                                          Gather#208 ends at 328.316µs
                                      Gather#212 step 7/10 (+5.096µs): 2.461µs self time
                                      Gather#212 step 8/10 (+7.557µs): scatter:
                                        Task#201: pool=0
                                        Task#201 step 1/2 (+0s): 78.091µs self time
                                        Task#201 step 2/2 (+78.091µs): return nil
                                        Task#201 ends at 298.792µs
                                          Gather#201: index=5
                                          Gather#201 step 1/2 (+0s): 9.996µs self time
                                          Gather#201 step 2/2 (+9.996µs): return nil
                                          Gather#201 ends at 308.788µs
                                      Gather#212 step 9/10 (+7.557µs): 2.447µs self time
                                      Gather#212 step 10/10 (+10.004µs): return nil
                                      Gather#212 ends at 223.148µs
                                  Gather#213 step 7/12 (+8.93µs): 479ns self time
                                  Gather#213 step 8/12 (+9.409µs): scatter:
                                    Task#211: pool=0
                                    Task#211 step 1/2 (+0s): 99.981µs self time
                                    Task#211 step 2/2 (+99.981µs): return nil
                                    Task#211 ends at 313.604µs
                                      Gather#211: index=3
                                      Gather#211 step 1/6 (+0s): 3.332µs self time
                                      Gather#211 step 2/6 (+3.332µs): scatter:
                                        Task#203: pool=0
                                        Task#203 step 1/2 (+0s): 99.994µs self time
                                        Task#203 step 2/2 (+99.994µs): return nil
                                        Task#203 ends at 416.93µs
                                          Gather#203: index=0
                                          Gather#203 step 1/2 (+0s): 10.486µs self time
                                          Gather#203 step 2/2 (+10.486µs): return nil
                                          Gather#203 ends at 427.416µs
                                      Gather#211 step 3/6 (+3.332µs): 3.328µs self time
                                      Gather#211 step 4/6 (+6.66µs): scatter:
                                        Task#204: pool=0
                                        Task#204 step 1/2 (+0s): 99.998µs self time
                                        Task#204 step 2/2 (+99.998µs): return nil
                                        Task#204 ends at 420.262µs
                                          Gather#204: index=3
                                          Gather#204 step 1/2 (+0s): 9.997µs self time
                                          Gather#204 step 2/2 (+9.997µs): return nil
                                          Gather#204 ends at 430.259µs
                                      Gather#211 step 5/6 (+6.66µs): 3.344µs self time
                                      Gather#211 step 6/6 (+10.004µs): return error
                                      Gather#211 ends at 323.608µs
                                  Gather#213 step 9/12 (+9.409µs): 294ns self time
                                  Gather#213 step 10/12 (+9.703µs): scatter:
                                    Task#210: pool=0
                                    Task#210 step 1/2 (+0s): 99.938µs self time
                                    Task#210 step 2/2 (+99.938µs): return nil
                                    Task#210 ends at 313.855µs
                                      Gather#210: index=0
                                      Gather#210 step 1/4 (+0s): 5.883µs self time
                                      Gather#210 step 2/4 (+5.883µs): scatter:
                                        Task#199: pool=0
                                        Task#199 step 1/2 (+0s): 99.995µs self time
                                        Task#199 step 2/2 (+99.995µs): return nil
                                        Task#199 ends at 419.733µs
                                          Gather#199: index=3
                                          Gather#199 step 1/2 (+0s): 10.031µs self time
                                          Gather#199 step 2/2 (+10.031µs): return error
                                          Gather#199 ends at 429.764µs
                                      Gather#210 step 3/4 (+5.883µs): 5.882µs self time
                                      Gather#210 step 4/4 (+11.765µs): return nil
                                      Gather#210 ends at 325.62µs
                                  Gather#213 step 11/12 (+9.703µs): 295ns self time
                                  Gather#213 step 12/12 (+9.998µs): return nil
                                  Gather#213 ends at 214.212µs
                              Gather#217 step 3/4 (+4.212µs): 4.211µs self time
                              Gather#217 step 4/4 (+8.423µs): return nil
                              Gather#217 ends at 108.427µs
                          Plan#13 step 2/4 (+0s): scatter:
                            Task#218: pool=0
                            Task#218 step 1/2 (+0s): 100.756µs self time
                            Task#218 step 2/2 (+100.756µs): return nil
                            Task#218 ends at 100.756µs
                              Gather#218: index=3
                              Gather#218 step 1/6 (+0s): 3.323µs self time
                              Gather#218 step 2/6 (+3.323µs): scatter:
                                Task#198: pool=0
                                Task#198 step 1/2 (+0s): 1.818956ms self time
                                Task#198 step 2/2 (+1.818956ms): return nil
                                Task#198 ends at 1.923035ms
                                  Gather#198: index=0
                                  Gather#198 step 1/2 (+0s): 6.482µs self time
                                  Gather#198 step 2/2 (+6.482µs): return nil
                                  Gather#198 ends at 1.929517ms
                              Gather#218 step 3/6 (+3.323µs): 3.336µs self time
                              Gather#218 step 4/6 (+6.659µs): scatter:
                                Task#215: pool=0
                                Task#215 step 1/2 (+0s): 95.991µs self time
                                Task#215 step 2/2 (+95.991µs): return nil
                                Task#215 ends at 203.406µs
                                  Gather#215: index=2
                                  Gather#215 step 1/4 (+0s): 498ns self time
                                  Gather#215 step 2/4 (+498ns): scatter:
                                    Task#205: pool=0
                                    Task#205 step 1/2 (+0s): 80.998µs self time
                                    Task#205 step 2/2 (+80.998µs): return nil
                                    Task#205 ends at 284.902µs
                                      Gather#205: index=2
                                      Gather#205 step 1/2 (+0s): 0s self time
                                      Gather#205 step 2/2 (+0s): return nil
                                      Gather#205 ends at 284.902µs
                                  Gather#215 step 3/4 (+498ns): 433ns self time
                                  Gather#215 step 4/4 (+931ns): return nil
                                  Gather#215 ends at 204.337µs
                              Gather#218 step 5/6 (+6.659µs): 3.34µs self time
                              Gather#218 step 6/6 (+9.999µs): return nil
                              Gather#218 ends at 110.755µs
                          Plan#13 step 3/4 (+0s): scatter:
                            Task#216: pool=0
                            Task#216 step 1/2 (+0s): 99.998µs self time
                            Task#216 step 2/2 (+99.998µs): return nil
                            Task#216 ends at 99.998µs
                              Gather#216: index=5
                              Gather#216 step 1/4 (+0s): 4.985µs self time
                              Gather#216 step 2/4 (+4.985µs): scatter:
                                Task#214: pool=0
                                Task#214 step 1/2 (+0s): 99.996µs self time
                                Task#214 step 2/2 (+99.996µs): return nil
                                Task#214 ends at 204.979µs
                                  Gather#214: index=6
                                  Gather#214 step 1/4 (+0s): 4.938µs self time
                                  Gather#214 step 2/4 (+4.938µs): scatter:
                                    Task#196: pool=0
                                    Task#196 step 1/2 (+0s): 99.643µs self time
                                    Task#196 step 2/2 (+99.643µs): return nil
                                    Task#196 ends at 309.56µs
                                      Combine#196: index=4 flush=<nil>
                                      Combine#196 step 1/2 (+0s): 9.992µs self time
                                      Combine#196 step 2/2 (+9.992µs): return nil
                                      Combine#196 ends at 319.552µs
                                  Gather#214 step 3/4 (+4.938µs): 4.936µs self time
                                  Gather#214 step 4/4 (+9.874µs): return nil
                                  Gather#214 ends at 214.853µs
                              Gather#216 step 3/4 (+4.985µs): 5.014µs self time
                              Gather#216 step 4/4 (+9.999µs): return nil
                              Gather#216 ends at 109.997µs
                          Plan#13 step 4/4 (+0s): ends at 7.826039ms
                        Gather#195 step 3/8 (+7.831316ms): 5.286µs self time
                        Gather#195 step 4/8 (+7.836602ms): scatter:
                          Task#194: pool=0
                          Task#194 step 1/2 (+0s): 99.992µs self time
                          Task#194 step 2/2 (+99.992µs): return error
                          Task#194 ends at 8.036675ms
                            Gather#194: index=13
                            Gather#194 step 1/4 (+0s): 4.996µs self time
                            Gather#194 step 2/4 (+4.996µs): scatter:
                              Task#193: pool=0
                              Task#193 step 1/2 (+0s): 100.091µs self time
                              Task#193 step 2/2 (+100.091µs): return error
                              Task#193 ends at 8.141762ms
                                Gather#193: index=5
                                Gather#193 step 1/6 (+0s): 167ns self time
                                Gather#193 step 2/6 (+167ns): scatter:
                                  Task#192: pool=0
                                  Task#192 step 1/2 (+0s): 51.346µs self time
                                  Task#192 step 2/2 (+51.346µs): return nil
                                  Task#192 ends at 8.193275ms
                                    Gather#192: index=0
                                    Gather#192 step 1/4 (+0s): 2.455µs self time
                                    Gather#192 step 2/4 (+2.455µs): scatter:
                                      Task#186: pool=0
                                      Task#186 step 1/2 (+0s): 99.998µs self time
                                      Task#186 step 2/2 (+99.998µs): return nil
                                      Task#186 ends at 8.295728ms
                                        Gather#186: index=3
                                        Gather#186 step 1/2 (+0s): 11.692µs self time
                                        Gather#186 step 2/2 (+11.692µs): return nil
                                        Gather#186 ends at 8.30742ms
                                    Gather#192 step 3/4 (+2.455µs): 7.544µs self time
                                    Gather#192 step 4/4 (+9.999µs): return error
                                    Gather#192 ends at 8.203274ms
                                Gather#193 step 3/6 (+167ns): 3.022µs self time
                                Gather#193 step 4/6 (+3.189µs): scatter:
                                  Task#191: pool=0
                                  Task#191 step 1/2 (+0s): 99.998µs self time
                                  Task#191 step 2/2 (+99.998µs): return nil
                                  Task#191 ends at 8.244949ms
                                    Gather#191: index=12
                                    Gather#191 step 1/4 (+0s): 7.127µs self time
                                    Gather#191 step 2/4 (+7.127µs): scatter:
                                      Task#190: pool=0
                                      Task#190 step 1/2 (+0s): 99.998µs self time
                                      Task#190 step 2/2 (+99.998µs): return nil
                                      Task#190 ends at 8.352074ms
                                        Gather#190: index=12
                                        Gather#190 step 1/4 (+0s): 4.977µs self time
                                        Gather#190 step 2/4 (+4.977µs): scatter:
                                          Task#189: pool=0
                                          Task#189 step 1/2 (+0s): 99.896µs self time
                                          Task#189 step 2/2 (+99.896µs): return nil
                                          Task#189 ends at 8.456947ms
                                            Gather#189: index=4
                                            Gather#189 step 1/2 (+0s): 9.957µs self time
                                            Gather#189 step 2/2 (+9.957µs): return nil
                                            Gather#189 ends at 8.466904ms
                                        Gather#190 step 3/4 (+4.977µs): 4.974µs self time
                                        Gather#190 step 4/4 (+9.951µs): return nil
                                        Gather#190 ends at 8.362025ms
                                    Gather#191 step 3/4 (+7.127µs): 2.76µs self time
                                    Gather#191 step 4/4 (+9.887µs): return nil
                                    Gather#191 ends at 8.254836ms
                                Gather#193 step 5/6 (+3.189µs): 2.443µs self time
                                Gather#193 step 6/6 (+5.632µs): return error
                                Gather#193 ends at 8.147394ms
                            Gather#194 step 3/4 (+4.996µs): 4.999µs self time
                            Gather#194 step 4/4 (+9.995µs): return nil
                            Gather#194 ends at 8.04667ms
                        Gather#195 step 5/8 (+7.836602ms): 2.443µs self time
                        Gather#195 step 6/8 (+7.839045ms): scatter:
                          Task#188: pool=0
                          Task#188 step 1/2 (+0s): 22.314058ms self time
                          Task#188 step 2/2 (+22.314058ms): return nil
                          Task#188 ends at 30.253184ms
                            Gather#188: index=7
                            Gather#188 step 1/2 (+0s): 9.464µs self time
                            Gather#188 step 2/2 (+9.464µs): return nil
                            Gather#188 ends at 30.262648ms
                        Gather#195 step 7/8 (+7.839045ms): 8.137µs self time
                        Gather#195 step 8/8 (+7.847182ms): return nil
                        Gather#195 ends at 7.947263ms
                    Plan#12 step 3/3 (+0s): ends at 30.262648ms
                  Task#185 step 3/4 (+30.312662ms): 49.985µs self time
                  Task#185 step 4/4 (+30.362647ms): return error
                  Task#185 ends at 30.530397ms
                    Gather#185: index=0
                    Gather#185 step 1/2 (+0s): 9.754µs self time
                    Gather#185 step 2/2 (+9.754µs): return nil
                    Gather#185 ends at 30.540151ms
                Combine#238 step 3/6 (+3.222µs): 2.29µs self time
                Combine#238 step 4/6 (+5.512µs): scatter:
                  Task#237: pool=1
                  Task#237 step 1/2 (+0s): 99.9µs self time
                  Task#237 step 2/2 (+99.9µs): return nil
                  Task#237 ends at 269.94µs
                    Gather#237: index=0
                    Gather#237 step 1/4 (+0s): 127.181µs self time
                    Gather#237 step 2/4 (+127.181µs): scatter:
                      Task#236: pool=0
                      Task#236 step 1/2 (+0s): 101.805µs self time
                      Task#236 step 2/2 (+101.805µs): return nil
                      Task#236 ends at 498.926µs
                        Gather#236: index=0
                        Gather#236 step 1/8 (+0s): 2.675µs self time
                        Gather#236 step 2/8 (+2.675µs): scatter:
                          Task#223: pool=0
                          Task#223 step 1/4 (+0s): 49.969µs self time
                          Task#223 step 2/4 (+49.969µs): subjob:
                            Plan#14: pathCount=5 taskCount=12 maxPathDuration=1.32499ms minGatherCount=12 maxGatherCount=12
                               TaskPools[0]: TaskPool#45: limit=4
                               TaskPools[1]: TaskPool#46: limit=10
                               TaskPools[2]: TaskPool#47: limit=1
                               TaskPools[3]: TaskPool#48: limit=1
                               TaskPools[4]: TaskPool#49: limit=1
                               TaskPools[5]: TaskPool#50: limit=10
                               TaskPools[6]: TaskPool#51: limit=2
                               TaskPools[7]: TaskPool#52: limit=2
                               CombinerPools[0]: CombinerPool#53: limit=1
                               CombinerPools[1]: CombinerPool#54: limit=2
                               Combiners[0]: pool=1
                               Combiners[1]: pool=0
                            Plan#14 step 1/4 (+0s): scatter:
                              Task#233: pool=6
                              Task#233 step 1/2 (+0s): 0s self time
                              Task#233 step 2/2 (+0s): return nil
                              Task#233 ends at 0s
                                Gather#233: index=0
                                Gather#233 step 1/4 (+0s): 659.453µs self time
                                Gather#233 step 2/4 (+659.453µs): scatter:
                                  Task#232: pool=3
                                  Task#232 step 1/2 (+0s): 99.997µs self time
                                  Task#232 step 2/2 (+99.997µs): return nil
                                  Task#232 ends at 759.45µs
                                    Gather#232: index=0
                                    Gather#232 step 1/8 (+0s): 2.458µs self time
                                    Gather#232 step 2/8 (+2.458µs): scatter:
                                      Task#230: pool=3
                                      Task#230 step 1/2 (+0s): 99.999µs self time
                                      Task#230 step 2/2 (+99.999µs): return nil
                                      Task#230 ends at 861.907µs
                                        Gather#230: index=0
                                        Gather#230 step 1/4 (+0s): 6.918µs self time
                                        Gather#230 step 2/4 (+6.918µs): scatter:
                                          Task#229: pool=2
                                          Task#229 step 1/2 (+0s): 251.097µs self time
                                          Task#229 step 2/2 (+251.097µs): return nil
                                          Task#229 ends at 1.119922ms
                                            Gather#229: index=0
                                            Gather#229 step 1/4 (+0s): 4.992µs self time
                                            Gather#229 step 2/4 (+4.992µs): scatter:
                                              Task#226: pool=6
                                              Task#226 step 1/2 (+0s): 99.996µs self time
                                              Task#226 step 2/2 (+99.996µs): return nil
                                              Task#226 ends at 1.22491ms
                                                Gather#226: index=0
                                                Gather#226 step 1/2 (+0s): 10.222µs self time
                                                Gather#226 step 2/2 (+10.222µs): return nil
                                                Gather#226 ends at 1.235132ms
                                            Gather#229 step 3/4 (+4.992µs): 5.009µs self time
                                            Gather#229 step 4/4 (+10.001µs): return nil
                                            Gather#229 ends at 1.129923ms
                                        Gather#230 step 3/4 (+6.918µs): 6.931µs self time
                                        Gather#230 step 4/4 (+13.849µs): return nil
                                        Gather#230 ends at 875.756µs
                                    Gather#232 step 3/8 (+2.458µs): 4.612µs self time
                                    Gather#232 step 4/8 (+7.07µs): scatter:
                                      Task#228: pool=2
                                      Task#228 step 1/2 (+0s): 115.112µs self time
                                      Task#228 step 2/2 (+115.112µs): return nil
                                      Task#228 ends at 881.632µs
                                        Gather#228: index=0
                                        Gather#228 step 1/2 (+0s): 72.096µs self time
                                        Gather#228 step 2/2 (+72.096µs): return nil
                                        Gather#228 ends at 953.728µs
                                    Gather#232 step 5/8 (+7.07µs): 1.485µs self time
                                    Gather#232 step 6/8 (+8.555µs): scatter:
                                      Task#225: pool=0
                                      Task#225 step 1/2 (+0s): 99.938µs self time
                                      Task#225 step 2/2 (+99.938µs): return nil
                                      Task#225 ends at 867.943µs
                                        Gather#225: index=0
                                        Gather#225 step 1/2 (+0s): 66.014µs self time
                                        Gather#225 step 2/2 (+66.014µs): return nil
                                        Gather#225 ends at 933.957µs
                                    Gather#232 step 7/8 (+8.555µs): 1.482µs self time
                                    Gather#232 step 8/8 (+10.037µs): return nil
                                    Gather#232 ends at 769.487µs
                                Gather#233 step 3/4 (+659.453µs): 665.537µs self time
                                Gather#233 step 4/4 (+1.32499ms): return nil
                                Gather#233 ends at 1.32499ms
                            Plan#14 step 2/4 (+0s): scatter:
                              Task#235: pool=2
                              Task#235 step 1/2 (+0s): 100.002µs self time
                              Task#235 step 2/2 (+100.002µs): return nil
                              Task#235 ends at 100.002µs
                                Gather#235: index=0
                                Gather#235 step 1/4 (+0s): 4.998µs self time
                                Gather#235 step 2/4 (+4.998µs): scatter:
                                  Task#231: pool=1
                                  Task#231 step 1/2 (+0s): 175.563µs self time
                                  Task#231 step 2/2 (+175.563µs): return nil
                                  Task#231 ends at 280.563µs
                                    Gather#231: index=0
                                    Gather#231 step 1/4 (+0s): 6.714µs self time
                                    Gather#231 step 2/4 (+6.714µs): scatter:
                                      Task#224: pool=1
                                      Task#224 step 1/2 (+0s): 99.948µs self time
                                      Task#224 step 2/2 (+99.948µs): return nil
                                      Task#224 ends at 387.225µs
                                        Gather#224: index=0
                                        Gather#224 step 1/2 (+0s): 9.984µs self time
                                        Gather#224 step 2/2 (+9.984µs): return nil
                                        Gather#224 ends at 397.209µs
                                    Gather#231 step 3/4 (+6.714µs): 3.314µs self time
                                    Gather#231 step 4/4 (+10.028µs): return nil
                                    Gather#231 ends at 290.591µs
                                Gather#235 step 3/4 (+4.998µs): 5.039µs self time
                                Gather#235 step 4/4 (+10.037µs): return error
                                Gather#235 ends at 110.039µs
                            Plan#14 step 3/4 (+0s): scatter:
                              Task#234: pool=2
                              Task#234 step 1/2 (+0s): 99.999µs self time
                              Task#234 step 2/2 (+99.999µs): return error
                              Task#234 ends at 99.999µs
                                Gather#234: index=0
                                Gather#234 step 1/4 (+0s): 4.164µs self time
                                Gather#234 step 2/4 (+4.164µs): scatter:
                                  Task#227: pool=5
                                  Task#227 step 1/2 (+0s): 100.848µs self time
                                  Task#227 step 2/2 (+100.848µs): return nil
                                  Task#227 ends at 205.011µs
                                    Gather#227: index=0
                                    Gather#227 step 1/2 (+0s): 10.002µs self time
                                    Gather#227 step 2/2 (+10.002µs): return nil
                                    Gather#227 ends at 215.013µs
                                Gather#234 step 3/4 (+4.164µs): 4.164µs self time
                                Gather#234 step 4/4 (+8.328µs): return nil
                                Gather#234 ends at 108.327µs
                            Plan#14 step 4/4 (+0s): ends at 1.32499ms
                          Task#223 step 3/4 (+1.374959ms): 49.978µs self time
                          Task#223 step 4/4 (+1.424937ms): return nil
                          Task#223 ends at 1.926538ms
                            Gather#223: index=2
                            Gather#223 step 1/4 (+0s): 3.721µs self time
                            Gather#223 step 2/4 (+3.721µs): scatter:
                              Task#220: pool=0
                              Task#220 step 1/2 (+0s): 99.98µs self time
                              Task#220 step 2/2 (+99.98µs): return nil
                              Task#220 ends at 2.030239ms
                                Gather#220: index=7
                                Gather#220 step 1/2 (+0s): 19.852µs self time
                                Gather#220 step 2/2 (+19.852µs): return nil
                                Gather#220 ends at 2.050091ms
                            Gather#223 step 3/4 (+3.721µs): 3.718µs self time
                            Gather#223 step 4/4 (+7.439µs): return nil
                            Gather#223 ends at 1.933977ms
                        Gather#236 step 3/8 (+2.675µs): 2.44µs self time
                        Gather#236 step 4/8 (+5.115µs): scatter:
                          Task#222: pool=0
                          Task#222 step 1/2 (+0s): 99.999µs self time
                          Task#222 step 2/2 (+99.999µs): return nil
                          Task#222 ends at 604.04µs
                            Gather#222: index=7
                            Gather#222 step 1/4 (+0s): 825ns self time
                            Gather#222 step 2/4 (+825ns): scatter:
                              Task#221: pool=0
                              Task#221 step 1/2 (+0s): 99.997µs self time
                              Task#221 step 2/2 (+99.997µs): return error
                              Task#221 ends at 704.862µs
                                Gather#221: index=0
                                Gather#221 step 1/2 (+0s): 7.723µs self time
                                Gather#221 step 2/2 (+7.723µs): return nil
                                Gather#221 ends at 712.585µs
                            Gather#222 step 3/4 (+825ns): 1.348µs self time
                            Gather#222 step 4/4 (+2.173µs): return nil
                            Gather#222 ends at 606.213µs
                        Gather#236 step 5/8 (+5.115µs): 2.118µs self time
                        Gather#236 step 6/8 (+7.233µs): scatter:
                          Task#182: pool=1
                          Task#182 step 1/2 (+0s): 100.001µs self time
                          Task#182 step 2/2 (+100.001µs): return nil
                          Task#182 ends at 606.16µs
                            Gather#182: index=8
                            Gather#182 step 1/2 (+0s): 3.258854ms self time
                            Gather#182 step 2/2 (+3.258854ms): return nil
                            Gather#182 ends at 3.865014ms
                        Gather#236 step 7/8 (+7.233µs): 2.764µs self time
                        Gather#236 step 8/8 (+9.997µs): return nil
                        Gather#236 ends at 508.923µs
                    Gather#237 step 3/4 (+127.181µs): 127.174µs self time
                    Gather#237 step 4/4 (+254.355µs): return nil
                    Gather#237 ends at 524.295µs
                Combine#238 step 5/6 (+5.512µs): 4.251µs self time
                Combine#238 step 6/6 (+9.763µs): return nil
                Combine#238 ends at 174.291µs
            Gather#240 step 3/4 (+4.937µs): 5.025µs self time
            Gather#240 step 4/4 (+9.962µs): return nil
            Gather#240 ends at 109.96µs
        Plan#11 step 4/4 (+0s): ends at 196.994999ms
      Task#181 step 3/4 (+197.044965ms): 50.026µs self time
      Task#181 step 4/4 (+197.094991ms): return nil
      Task#181 ends at 197.199708ms
        Gather#181: index=4
        Gather#181 step 1/2 (+0s): 10.012µs self time
        Gather#181 step 2/2 (+10.012µs): return nil
        Gather#181 ends at 197.20972ms
    Gather#327 step 5/8 (+4.721µs): 2.547µs self time
    Gather#327 step 6/8 (+7.268µs): scatter:
      Task#326: pool=1
      Task#326 step 1/2 (+0s): 100.001µs self time
      Task#326 step 2/2 (+100.001µs): return nil
      Task#326 ends at 207.265µs
        Gather#326: index=5
        Gather#326 step 1/6 (+0s): 3.616µs self time
        Gather#326 step 2/6 (+3.616µs): scatter:
          Task#325: pool=0
          Task#325 step 1/2 (+0s): 85.527µs self time
          Task#325 step 2/2 (+85.527µs): return nil
          Task#325 ends at 296.408µs
            Gather#325: index=0
            Gather#325 step 1/4 (+0s): 845ns self time
            Gather#325 step 2/4 (+845ns): scatter:
              Task#324: pool=1
              Task#324 step 1/2 (+0s): 100.002µs self time
              Task#324 step 2/2 (+100.002µs): return nil
              Task#324 ends at 397.255µs
                Gather#324: index=4
                Gather#324 step 1/2 (+0s): 10µs self time
                Gather#324 step 2/2 (+10µs): return nil
                Gather#324 ends at 407.255µs
            Gather#325 step 3/4 (+845ns): 9.158µs self time
            Gather#325 step 4/4 (+10.003µs): return nil
            Gather#325 ends at 306.411µs
        Gather#326 step 3/6 (+3.616µs): 3.632µs self time
        Gather#326 step 4/6 (+7.248µs): scatter:
          Task#180: pool=1
          Task#180 step 1/2 (+0s): 100.003µs self time
          Task#180 step 2/2 (+100.003µs): return nil
          Task#180 ends at 314.516µs
            Gather#180: index=0
            Gather#180 step 1/2 (+0s): 9.996µs self time
            Gather#180 step 2/2 (+9.996µs): return nil
            Gather#180 ends at 324.512µs
        Gather#326 step 5/6 (+7.248µs): 3.637µs self time
        Gather#326 step 6/6 (+10.885µs): return nil
        Gather#326 ends at 218.15µs
    Gather#327 step 7/8 (+7.268µs): 2.554µs self time
    Gather#327 step 8/8 (+9.822µs): return nil
    Gather#327 ends at 109.818µs
Plan#0 step 2/2 (+0s): ends at 281.799482ms`

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
