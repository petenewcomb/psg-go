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

	expected := `Plan#0: pathCount=9 taskCount=17 maxPathDuration=29.100037ms minGatherCount=13 maxGatherCount=29
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
Plan#0 step 1/4 (+0s): scatter:
  Task#369: pool=1
  Task#369 step 1/2 (+0s): 9.962µs self time
  Task#369 step 2/2 (+9.962µs): return nil
  Task#369 ends at 9.962µs
    Combine#369: index=13 flush=Gather#369
    Combine#369 step 1/2 (+0s): 1.005µs self time
    Combine#369 step 2/2 (+1.005µs): return nil
    Combine#369 ends at 10.967µs
      Gather#369: index=5
      Gather#369 step 1/6 (+0s): 472ns self time
      Gather#369 step 2/6 (+472ns): subjob:
        Plan#15: pathCount=1 taskCount=4 maxPathDuration=35.882µs minGatherCount=2 maxGatherCount=20
           TaskPools[0]: TaskPool#75: limit=2
           TaskPools[1]: TaskPool#76: limit=1
           TaskPools[2]: TaskPool#77: limit=5
           CombinerPools[0]: CombinerPool#61: limit=6
           CombinerPools[1]: CombinerPool#62: limit=4
           CombinerPools[2]: CombinerPool#63: limit=9
           CombinerPools[3]: CombinerPool#64: limit=2
           CombinerPools[4]: CombinerPool#65: limit=1
           Combiners[0]: pool=1
           Combiners[1]: pool=2
           Combiners[2]: pool=1
           Combiners[3]: pool=1
        Plan#15 step 1/2 (+0s): scatter:
          Task#373: pool=0
          Task#373 step 1/2 (+0s): 1.238µs self time
          Task#373 step 2/2 (+1.238µs): return nil
          Task#373 ends at 1.238µs
            Combine#373: index=1 flush=<nil>
            Combine#373 step 1/4 (+0s): 434ns self time
            Combine#373 step 2/4 (+434ns): scatter:
              Task#372: pool=2
              Task#372 step 1/2 (+0s): 10µs self time
              Task#372 step 2/2 (+10µs): return nil
              Task#372 ends at 11.672µs
                Gather#372: index=0
                Gather#372 step 1/4 (+0s): 551ns self time
                Gather#372 step 2/4 (+551ns): scatter:
                  Task#371: pool=1
                  Task#371 step 1/2 (+0s): 19.201µs self time
                  Task#371 step 2/2 (+19.201µs): return nil
                  Task#371 ends at 31.424µs
                    Combine#371: index=1 flush=<nil>
                    Combine#371 step 1/4 (+0s): 999ns self time
                    Combine#371 step 2/4 (+999ns): scatter:
                      Task#370: pool=2
                      Task#370 step 1/2 (+0s): 3.459µs self time
                      Task#370 step 2/2 (+3.459µs): return nil
                      Task#370 ends at 35.882µs
                        Gather#370: index=0
                        Gather#370 step 1/2 (+0s): 0s self time
                        Gather#370 step 2/2 (+0s): return nil
                        Gather#370 ends at 35.882µs
                    Combine#371 step 3/4 (+999ns): 0s self time
                    Combine#371 step 4/4 (+999ns): return nil
                    Combine#371 ends at 32.423µs
                Gather#372 step 3/4 (+551ns): 558ns self time
                Gather#372 step 4/4 (+1.109µs): return nil
                Gather#372 ends at 12.781µs
            Combine#373 step 3/4 (+434ns): 447ns self time
            Combine#373 step 4/4 (+881ns): return nil
            Combine#373 ends at 2.119µs
        Plan#15 step 2/2 (+0s): ends at 35.882µs
      Gather#369 step 3/6 (+36.354µs): 214ns self time
      Gather#369 step 4/6 (+36.568µs): scatter:
        Task#333: pool=0
        Task#333 step 1/2 (+0s): 9.998µs self time
        Task#333 step 2/2 (+9.998µs): return nil
        Task#333 ends at 0s
          Combine#333: index=13 flush=<nil>
          Combine#333 step 1/2 (+0s): 997ns self time
          Combine#333 step 2/2 (+997ns): return nil
          Combine#333 ends at 0s
      Gather#369 step 5/6 (+36.568µs): 288ns self time
      Gather#369 step 6/6 (+36.856µs): return nil
      Gather#369 ends at 0s
Plan#0 step 2/4 (+0s): scatter:
  Task#367: pool=1
  Task#367 step 1/2 (+0s): 2.307593ms self time
  Task#367 step 2/2 (+2.307593ms): return nil
  Task#367 ends at 2.307593ms
    Gather#367: index=1
    Gather#367 step 1/8 (+0s): 68ns self time
    Gather#367 step 2/8 (+68ns): scatter:
      Task#365: pool=1
      Task#365 step 1/2 (+0s): 410.031µs self time
      Task#365 step 2/2 (+410.031µs): return error
      Task#365 ends at 2.717692ms
        Gather#365: index=1
        Gather#365 step 1/10 (+0s): 0s self time
        Gather#365 step 2/10 (+0s): scatter:
          Task#364: pool=1
          Task#364 step 1/2 (+0s): 23.247µs self time
          Task#364 step 2/2 (+23.247µs): return nil
          Task#364 ends at 2.740939ms
            Combine#364: index=2 flush=<nil>
            Combine#364 step 1/4 (+0s): 768ns self time
            Combine#364 step 2/4 (+768ns): scatter:
              Task#363: pool=0
              Task#363 step 1/2 (+0s): 9.992µs self time
              Task#363 step 2/2 (+9.992µs): return nil
              Task#363 ends at 2.751699ms
                Gather#363: index=0
                Gather#363 step 1/4 (+0s): 466ns self time
                Gather#363 step 2/4 (+466ns): scatter:
                  Task#362: pool=1
                  Task#362 step 1/2 (+0s): 10.759µs self time
                  Task#362 step 2/2 (+10.759µs): return error
                  Task#362 ends at 2.762924ms
                    Gather#362: index=0
                    Gather#362 step 1/6 (+0s): 334ns self time
                    Gather#362 step 2/6 (+334ns): scatter:
                      Task#361: pool=0
                      Task#361 step 1/2 (+0s): 907ns self time
                      Task#361 step 2/2 (+907ns): return nil
                      Task#361 ends at 2.764165ms
                        Gather#361: index=1
                        Gather#361 step 1/2 (+0s): 2.494µs self time
                        Gather#361 step 2/2 (+2.494µs): return nil
                        Gather#361 ends at 2.766659ms
                    Gather#362 step 3/6 (+334ns): 281ns self time
                    Gather#362 step 4/6 (+615ns): scatter:
                      Task#334: pool=1
                      Task#334 step 1/2 (+0s): 5.527µs self time
                      Task#334 step 2/2 (+5.527µs): return nil
                      Task#334 ends at 2.769066ms
                        Gather#334: index=4
                        Gather#334 step 1/2 (+0s): 3.425µs self time
                        Gather#334 step 2/2 (+3.425µs): return nil
                        Gather#334 ends at 2.772491ms
                    Gather#362 step 5/6 (+615ns): 382ns self time
                    Gather#362 step 6/6 (+997ns): return nil
                    Gather#362 ends at 2.763921ms
                Gather#363 step 3/4 (+466ns): 88ns self time
                Gather#363 step 4/4 (+554ns): return nil
                Gather#363 ends at 2.752253ms
            Combine#364 step 3/4 (+768ns): 228ns self time
            Combine#364 step 4/4 (+996ns): return nil
            Combine#364 ends at 2.741935ms
        Gather#365 step 3/10 (+0s): 0s self time
        Gather#365 step 4/10 (+0s): scatter:
          Task#359: pool=1
          Task#359 step 1/2 (+0s): 9.978µs self time
          Task#359 step 2/2 (+9.978µs): return nil
          Task#359 ends at 2.72767ms
            Gather#359: index=1
            Gather#359 step 1/2 (+0s): 997ns self time
            Gather#359 step 2/2 (+997ns): return nil
            Gather#359 ends at 2.728667ms
        Gather#365 step 5/10 (+0s): 0s self time
        Gather#365 step 6/10 (+0s): scatter:
          Task#335: pool=0
          Task#335 step 1/2 (+0s): 9.998µs self time
          Task#335 step 2/2 (+9.998µs): return nil
          Task#335 ends at 2.72769ms
            Gather#335: index=0
            Gather#335 step 1/2 (+0s): 1.027µs self time
            Gather#335 step 2/2 (+1.027µs): return nil
            Gather#335 ends at 2.728717ms
        Gather#365 step 7/10 (+0s): 0s self time
        Gather#365 step 8/10 (+0s): scatter:
          Task#336: pool=0
          Task#336 step 1/2 (+0s): 0s self time
          Task#336 step 2/2 (+0s): return nil
          Task#336 ends at 2.717692ms
            Combine#336: index=5 flush=<nil>
            Combine#336 step 1/4 (+0s): 387ns self time
            Combine#336 step 2/4 (+387ns): subjob:
              Plan#14: pathCount=13 taskCount=22 maxPathDuration=9.914136ms minGatherCount=18 maxGatherCount=25
                 TaskPools[0]: TaskPool#65: limit=2
                 TaskPools[1]: TaskPool#66: limit=1
                 TaskPools[2]: TaskPool#67: limit=10
                 TaskPools[3]: TaskPool#68: limit=2
                 TaskPools[4]: TaskPool#69: limit=1
                 TaskPools[5]: TaskPool#70: limit=3
                 TaskPools[6]: TaskPool#71: limit=2
                 TaskPools[7]: TaskPool#72: limit=8
                 TaskPools[8]: TaskPool#73: limit=4
                 TaskPools[9]: TaskPool#74: limit=2
                 CombinerPools[0]: CombinerPool#59: limit=2
                 CombinerPools[1]: CombinerPool#60: limit=1
                 Combiners[0]: pool=0
                 Combiners[1]: pool=0
                 Combiners[2]: pool=0
                 Combiners[3]: pool=1
                 Combiners[4]: pool=1
                 Combiners[5]: pool=0
                 Combiners[6]: pool=1
                 Combiners[7]: pool=1
                 Combiners[8]: pool=1
                 Combiners[9]: pool=1
                 Combiners[10]: pool=1
                 Combiners[11]: pool=0
                 Combiners[12]: pool=0
                 Combiners[13]: pool=0
              Plan#14 step 1/4 (+0s): scatter:
                Task#358: pool=8
                Task#358 step 1/2 (+0s): 10.028µs self time
                Task#358 step 2/2 (+10.028µs): return nil
                Task#358 ends at 10.028µs
                  Gather#358: index=1
                  Gather#358 step 1/4 (+0s): 138ns self time
                  Gather#358 step 2/4 (+138ns): scatter:
                    Task#337: pool=3
                    Task#337 step 1/2 (+0s): 10.557µs self time
                    Task#337 step 2/2 (+10.557µs): return nil
                    Task#337 ends at 20.723µs
                      Gather#337: index=3
                      Gather#337 step 1/2 (+0s): 901ns self time
                      Gather#337 step 2/2 (+901ns): return nil
                      Gather#337 ends at 21.624µs
                  Gather#358 step 3/4 (+138ns): 139ns self time
                  Gather#358 step 4/4 (+277ns): return nil
                  Gather#358 ends at 10.305µs
              Plan#14 step 2/4 (+0s): scatter:
                Task#357: pool=1
                Task#357 step 1/2 (+0s): 10.006µs self time
                Task#357 step 2/2 (+10.006µs): return nil
                Task#357 ends at 10.006µs
                  Combine#357: index=2 flush=<nil>
                  Combine#357 step 1/12 (+0s): 106ns self time
                  Combine#357 step 2/12 (+106ns): scatter:
                    Task#338: pool=6
                    Task#338 step 1/2 (+0s): 10.002µs self time
                    Task#338 step 2/2 (+10.002µs): return nil
                    Task#338 ends at 20.114µs
                      Combine#338: index=6 flush=<nil>
                      Combine#338 step 1/2 (+0s): 1.141µs self time
                      Combine#338 step 2/2 (+1.141µs): return nil
                      Combine#338 ends at 21.255µs
                  Combine#357 step 3/12 (+106ns): 181ns self time
                  Combine#357 step 4/12 (+287ns): scatter:
                    Task#343: pool=9
                    Task#343 step 1/2 (+0s): 10.008µs self time
                    Task#343 step 2/2 (+10.008µs): return nil
                    Task#343 ends at 20.301µs
                      Combine#343: index=10 flush=Gather#343
                      Combine#343 step 1/2 (+0s): 893ns self time
                      Combine#343 step 2/2 (+893ns): return error
                      Combine#343 ends at 21.194µs
                        Gather#343: index=2
                        Gather#343 step 1/2 (+0s): 8.251µs self time
                        Gather#343 step 2/2 (+8.251µs): return nil
                        Gather#343 ends at 0s
                  Combine#357 step 5/12 (+287ns): 378ns self time
                  Combine#357 step 6/12 (+665ns): scatter:
                    Task#354: pool=6
                    Task#354 step 1/2 (+0s): 10.004µs self time
                    Task#354 step 2/2 (+10.004µs): return nil
                    Task#354 ends at 20.675µs
                      Combine#354: index=1 flush=<nil>
                      Combine#354 step 1/8 (+0s): 140.844µs self time
                      Combine#354 step 2/8 (+140.844µs): scatter:
                        Task#339: pool=9
                        Task#339 step 1/2 (+0s): 2.26µs self time
                        Task#339 step 2/2 (+2.26µs): return nil
                        Task#339 ends at 163.779µs
                          Gather#339: index=3
                          Gather#339 step 1/2 (+0s): 259ns self time
                          Gather#339 step 2/2 (+259ns): return error
                          Gather#339 ends at 164.038µs
                      Combine#354 step 3/8 (+140.844µs): 286.384µs self time
                      Combine#354 step 4/8 (+427.228µs): scatter:
                        Task#352: pool=6
                        Task#352 step 1/2 (+0s): 10.046µs self time
                        Task#352 step 2/2 (+10.046µs): return nil
                        Task#352 ends at 457.949µs
                          Gather#352: index=0
                          Gather#352 step 1/4 (+0s): 513ns self time
                          Gather#352 step 2/4 (+513ns): scatter:
                            Task#351: pool=3
                            Task#351 step 1/2 (+0s): 9.992µs self time
                            Task#351 step 2/2 (+9.992µs): return nil
                            Task#351 ends at 468.454µs
                              Gather#351: index=3
                              Gather#351 step 1/4 (+0s): 289.257µs self time
                              Gather#351 step 2/4 (+289.257µs): scatter:
                                Task#350: pool=4
                                Task#350 step 1/2 (+0s): 135.272µs self time
                                Task#350 step 2/2 (+135.272µs): return nil
                                Task#350 ends at 892.983µs
                                  Gather#350: index=0
                                  Gather#350 step 1/8 (+0s): 263ns self time
                                  Gather#350 step 2/8 (+263ns): scatter:
                                    Task#347: pool=1
                                    Task#347 step 1/2 (+0s): 9.996µs self time
                                    Task#347 step 2/2 (+9.996µs): return nil
                                    Task#347 ends at 903.242µs
                                      Gather#347: index=2
                                      Gather#347 step 1/2 (+0s): 132.449µs self time
                                      Gather#347 step 2/2 (+132.449µs): return nil
                                      Gather#347 ends at 1.035691ms
                                  Gather#350 step 3/8 (+263ns): 251ns self time
                                  Gather#350 step 4/8 (+514ns): scatter:
                                    Task#342: pool=6
                                    Task#342 step 1/2 (+0s): 6.408µs self time
                                    Task#342 step 2/2 (+6.408µs): return nil
                                    Task#342 ends at 899.905µs
                                      Gather#342: index=2
                                      Gather#342 step 1/2 (+0s): 374ns self time
                                      Gather#342 step 2/2 (+374ns): return nil
                                      Gather#342 ends at 900.279µs
                                  Gather#350 step 5/8 (+514ns): 498ns self time
                                  Gather#350 step 6/8 (+1.012µs): scatter:
                                    Task#344: pool=1
                                    Task#344 step 1/2 (+0s): 9.861µs self time
                                    Task#344 step 2/2 (+9.861µs): return nil
                                    Task#344 ends at 903.856µs
                                      Gather#344: index=2
                                      Gather#344 step 1/2 (+0s): 1.006µs self time
                                      Gather#344 step 2/2 (+1.006µs): return nil
                                      Gather#344 ends at 904.862µs
                                  Gather#350 step 7/8 (+1.012µs): 1ns self time
                                  Gather#350 step 8/8 (+1.013µs): return nil
                                  Gather#350 ends at 893.996µs
                              Gather#351 step 3/4 (+289.257µs): 289.27µs self time
                              Gather#351 step 4/4 (+578.527µs): return nil
                              Gather#351 ends at 1.046981ms
                          Gather#352 step 3/4 (+513ns): 515ns self time
                          Gather#352 step 4/4 (+1.028µs): return nil
                          Gather#352 ends at 458.977µs
                      Combine#354 step 5/8 (+427.228µs): 286.383µs self time
                      Combine#354 step 6/8 (+713.611µs): scatter:
                        Task#345: pool=1
                        Task#345 step 1/2 (+0s): 9.956µs self time
                        Task#345 step 2/2 (+9.956µs): return nil
                        Task#345 ends at 744.242µs
                          Gather#345: index=0
                          Gather#345 step 1/2 (+0s): 1.257µs self time
                          Gather#345 step 2/2 (+1.257µs): return error
                          Gather#345 ends at 745.499µs
                      Combine#354 step 7/8 (+713.611µs): 286.389µs self time
                      Combine#354 step 8/8 (+1ms): return nil
                      Combine#354 ends at 1.020675ms
                  Combine#357 step 7/12 (+665ns): 303ns self time
                  Combine#357 step 8/12 (+968ns): scatter:
                    Task#346: pool=9
                    Task#346 step 1/2 (+0s): 10.014µs self time
                    Task#346 step 2/2 (+10.014µs): return nil
                    Task#346 ends at 20.988µs
                      Gather#346: index=2
                      Gather#346 step 1/2 (+0s): 670ns self time
                      Gather#346 step 2/2 (+670ns): return nil
                      Gather#346 ends at 21.658µs
                  Combine#357 step 9/12 (+968ns): 0s self time
                  Combine#357 step 10/12 (+968ns): scatter:
                    Task#349: pool=4
                    Task#349 step 1/2 (+0s): 10.002µs self time
                    Task#349 step 2/2 (+10.002µs): return nil
                    Task#349 ends at 20.976µs
                      Gather#349: index=1
                      Gather#349 step 1/2 (+0s): 1.127µs self time
                      Gather#349 step 2/2 (+1.127µs): return nil
                      Gather#349 ends at 22.103µs
                  Combine#357 step 11/12 (+968ns): 40ns self time
                  Combine#357 step 12/12 (+1.008µs): return nil
                  Combine#357 ends at 11.014µs
              Plan#14 step 3/4 (+0s): scatter:
                Task#356: pool=0
                Task#356 step 1/2 (+0s): 10.012µs self time
                Task#356 step 2/2 (+10.012µs): return nil
                Task#356 ends at 10.012µs
                  Gather#356: index=2
                  Gather#356 step 1/8 (+0s): 277ns self time
                  Gather#356 step 2/8 (+277ns): scatter:
                    Task#341: pool=5
                    Task#341 step 1/2 (+0s): 9.90285ms self time
                    Task#341 step 2/2 (+9.90285ms): return nil
                    Task#341 ends at 9.913139ms
                      Gather#341: index=0
                      Gather#341 step 1/2 (+0s): 997ns self time
                      Gather#341 step 2/2 (+997ns): return nil
                      Gather#341 ends at 9.914136ms
                  Gather#356 step 3/8 (+277ns): 272ns self time
                  Gather#356 step 4/8 (+549ns): scatter:
                    Task#340: pool=9
                    Task#340 step 1/2 (+0s): 3.227µs self time
                    Task#340 step 2/2 (+3.227µs): return nil
                    Task#340 ends at 13.788µs
                      Gather#340: index=1
                      Gather#340 step 1/2 (+0s): 998ns self time
                      Gather#340 step 2/2 (+998ns): return nil
                      Gather#340 ends at 14.786µs
                  Gather#356 step 5/8 (+549ns): 272ns self time
                  Gather#356 step 6/8 (+821ns): scatter:
                    Task#355: pool=0
                    Task#355 step 1/2 (+0s): 10.006µs self time
                    Task#355 step 2/2 (+10.006µs): return nil
                    Task#355 ends at 20.839µs
                      Combine#355: index=13 flush=<nil>
                      Combine#355 step 1/4 (+0s): 934ns self time
                      Combine#355 step 2/4 (+934ns): scatter:
                        Task#353: pool=8
                        Task#353 step 1/2 (+0s): 10.859µs self time
                        Task#353 step 2/2 (+10.859µs): return nil
                        Task#353 ends at 32.632µs
                          Gather#353: index=0
                          Gather#353 step 1/4 (+0s): 526ns self time
                          Gather#353 step 2/4 (+526ns): scatter:
                            Task#348: pool=4
                            Task#348 step 1/2 (+0s): 3.767µs self time
                            Task#348 step 2/2 (+3.767µs): return nil
                            Task#348 ends at 36.925µs
                              Gather#348: index=3
                              Gather#348 step 1/2 (+0s): 1.96µs self time
                              Gather#348 step 2/2 (+1.96µs): return nil
                              Gather#348 ends at 38.885µs
                          Gather#353 step 3/4 (+526ns): 460ns self time
                          Gather#353 step 4/4 (+986ns): return nil
                          Gather#353 ends at 33.618µs
                      Combine#355 step 3/4 (+934ns): 65ns self time
                      Combine#355 step 4/4 (+999ns): return nil
                      Combine#355 ends at 21.838µs
                  Gather#356 step 7/8 (+821ns): 269ns self time
                  Gather#356 step 8/8 (+1.09µs): return nil
                  Gather#356 ends at 11.102µs
              Plan#14 step 4/4 (+0s): ends at 9.914136ms
            Combine#336 step 3/4 (+9.914523ms): 407ns self time
            Combine#336 step 4/4 (+9.91493ms): return nil
            Combine#336 ends at 12.632622ms
        Gather#365 step 9/10 (+0s): 0s self time
        Gather#365 step 10/10 (+0s): return nil
        Gather#365 ends at 2.717692ms
    Gather#367 step 3/8 (+68ns): 68ns self time
    Gather#367 step 4/8 (+136ns): scatter:
      Task#366: pool=0
      Task#366 step 1/2 (+0s): 17.22µs self time
      Task#366 step 2/2 (+17.22µs): return nil
      Task#366 ends at 2.324949ms
        Gather#366: index=2
        Gather#366 step 1/4 (+0s): 470ns self time
        Gather#366 step 2/4 (+470ns): scatter:
          Task#332: pool=1
          Task#332 step 1/2 (+0s): 10.894µs self time
          Task#332 step 2/2 (+10.894µs): return nil
          Task#332 ends at 2.336313ms
            Gather#332: index=0
            Gather#332 step 1/2 (+0s): 1.158µs self time
            Gather#332 step 2/2 (+1.158µs): return nil
            Gather#332 ends at 2.337471ms
        Gather#366 step 3/4 (+470ns): 473ns self time
        Gather#366 step 4/4 (+943ns): return nil
        Gather#366 ends at 2.325892ms
    Gather#367 step 5/8 (+136ns): 33ns self time
    Gather#367 step 6/8 (+169ns): scatter:
      Task#0: pool=1
      Task#0 step 1/2 (+0s): 10.001µs self time
      Task#0 step 2/2 (+10.001µs): return nil
      Task#0 ends at 2.317763ms
        Gather#0: index=3
        Gather#0 step 1/4 (+0s): 879ns self time
        Gather#0 step 2/4 (+879ns): subjob:
          Plan#1: pathCount=15 taskCount=28 maxPathDuration=26.781267ms minGatherCount=18 maxGatherCount=31
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
          Plan#1 step 1/3 (+0s): scatter:
            Task#330: pool=1
            Task#330 step 1/2 (+0s): 10.014µs self time
            Task#330 step 2/2 (+10.014µs): return nil
            Task#330 ends at 10.014µs
              Gather#330: index=3
              Gather#330 step 1/6 (+0s): 394ns self time
              Gather#330 step 2/6 (+394ns): scatter:
                Task#3: pool=7
                Task#3 step 1/2 (+0s): 10.082µs self time
                Task#3 step 2/2 (+10.082µs): return nil
                Task#3 ends at 20.49µs
                  Gather#3: index=2
                  Gather#3 step 1/2 (+0s): 1.015µs self time
                  Gather#3 step 2/2 (+1.015µs): return nil
                  Gather#3 ends at 21.505µs
              Gather#330 step 3/6 (+394ns): 209ns self time
              Gather#330 step 4/6 (+603ns): scatter:
                Task#329: pool=1
                Task#329 step 1/2 (+0s): 10.015µs self time
                Task#329 step 2/2 (+10.015µs): return nil
                Task#329 ends at 20.632µs
                  Combine#329: index=2 flush=Gather#329
                  Combine#329 step 1/4 (+0s): 2.634µs self time
                  Combine#329 step 2/4 (+2.634µs): scatter:
                    Task#69: pool=1
                    Task#69 step 1/2 (+0s): 10.018µs self time
                    Task#69 step 2/2 (+10.018µs): return nil
                    Task#69 ends at 33.284µs
                      Combine#69: index=0 flush=<nil>
                      Combine#69 step 1/2 (+0s): 937ns self time
                      Combine#69 step 2/2 (+937ns): return nil
                      Combine#69 ends at 34.221µs
                  Combine#329 step 3/4 (+2.634µs): 9.775µs self time
                  Combine#329 step 4/4 (+12.409µs): return nil
                  Combine#329 ends at 33.041µs
                    Gather#329: index=0
                    Gather#329 step 1/2 (+0s): 1.005µs self time
                    Gather#329 step 2/2 (+1.005µs): return nil
                    Gather#329 ends at 0s
              Gather#330 step 5/6 (+603ns): 385ns self time
              Gather#330 step 6/6 (+988ns): return nil
              Gather#330 ends at 11.002µs
          Plan#1 step 2/3 (+0s): scatter:
            Task#331: pool=8
            Task#331 step 1/2 (+0s): 10.01µs self time
            Task#331 step 2/2 (+10.01µs): return nil
            Task#331 ends at 10.01µs
              Gather#331: index=1
              Gather#331 step 1/10 (+0s): 127ns self time
              Gather#331 step 2/10 (+127ns): scatter:
                Task#273: pool=8
                Task#273 step 1/4 (+0s): 4.795µs self time
                Task#273 step 2/4 (+4.795µs): subjob:
                  Plan#12: pathCount=14 taskCount=28 maxPathDuration=10.245167ms minGatherCount=20 maxGatherCount=40
                     TaskPools[0]: TaskPool#60: limit=7
                     TaskPools[1]: TaskPool#61: limit=1
                     CombinerPools[0]: CombinerPool#53: limit=4
                     CombinerPools[1]: CombinerPool#54: limit=1
                     Combiners[0]: pool=0
                     Combiners[1]: pool=1
                     Combiners[2]: pool=0
                     Combiners[3]: pool=0
                     Combiners[4]: pool=0
                     Combiners[5]: pool=1
                     Combiners[6]: pool=0
                     Combiners[7]: pool=0
                     Combiners[8]: pool=0
                     Combiners[9]: pool=0
                     Combiners[10]: pool=1
                  Plan#12 step 1/4 (+0s): scatter:
                    Task#328: pool=1
                    Task#328 step 1/2 (+0s): 9.754µs self time
                    Task#328 step 2/2 (+9.754µs): return nil
                    Task#328 ends at 9.754µs
                      Gather#328: index=0
                      Gather#328 step 1/6 (+0s): 531ns self time
                      Gather#328 step 2/6 (+531ns): scatter:
                        Task#324: pool=1
                        Task#324 step 1/2 (+0s): 9.203µs self time
                        Task#324 step 2/2 (+9.203µs): return error
                        Task#324 ends at 19.488µs
                          Combine#324: index=10 flush=<nil>
                          Combine#324 step 1/4 (+0s): 287ns self time
                          Combine#324 step 2/4 (+287ns): scatter:
                            Task#281: pool=1
                            Task#281 step 1/2 (+0s): 10.001µs self time
                            Task#281 step 2/2 (+10.001µs): return nil
                            Task#281 ends at 29.776µs
                              Gather#281: index=0
                              Gather#281 step 1/2 (+0s): 986ns self time
                              Gather#281 step 2/2 (+986ns): return error
                              Gather#281 ends at 30.762µs
                          Combine#324 step 3/4 (+287ns): 737ns self time
                          Combine#324 step 4/4 (+1.024µs): return nil
                          Combine#324 ends at 20.512µs
                      Gather#328 step 3/6 (+531ns): 7ns self time
                      Gather#328 step 4/6 (+538ns): scatter:
                        Task#282: pool=0
                        Task#282 step 1/2 (+0s): 11.469µs self time
                        Task#282 step 2/2 (+11.469µs): return nil
                        Task#282 ends at 21.761µs
                          Combine#282: index=0 flush=<nil>
                          Combine#282 step 1/2 (+0s): 127.432µs self time
                          Combine#282 step 2/2 (+127.432µs): return nil
                          Combine#282 ends at 149.193µs
                      Gather#328 step 5/6 (+538ns): 461ns self time
                      Gather#328 step 6/6 (+999ns): return nil
                      Gather#328 ends at 10.753µs
                  Plan#12 step 2/4 (+0s): scatter:
                    Task#326: pool=0
                    Task#326 step 1/2 (+0s): 9.999µs self time
                    Task#326 step 2/2 (+9.999µs): return nil
                    Task#326 ends at 9.999µs
                      Combine#326: index=8 flush=<nil>
                      Combine#326 step 1/4 (+0s): 67ns self time
                      Combine#326 step 2/4 (+67ns): scatter:
                        Task#325: pool=0
                        Task#325 step 1/2 (+0s): 10.34µs self time
                        Task#325 step 2/2 (+10.34µs): return nil
                        Task#325 ends at 20.406µs
                          Combine#325: index=0 flush=<nil>
                          Combine#325 step 1/4 (+0s): 430ns self time
                          Combine#325 step 2/4 (+430ns): scatter:
                            Task#314: pool=0
                            Task#314 step 1/2 (+0s): 16.824µs self time
                            Task#314 step 2/2 (+16.824µs): return nil
                            Task#314 ends at 37.66µs
                              Gather#314: index=0
                              Gather#314 step 1/2 (+0s): 997ns self time
                              Gather#314 step 2/2 (+997ns): return nil
                              Gather#314 ends at 38.657µs
                          Combine#325 step 3/4 (+430ns): 421ns self time
                          Combine#325 step 4/4 (+851ns): return nil
                          Combine#325 ends at 21.257µs
                      Combine#326 step 3/4 (+67ns): 26ns self time
                      Combine#326 step 4/4 (+93ns): return nil
                      Combine#326 ends at 10.092µs
                  Plan#12 step 3/4 (+0s): scatter:
                    Task#327: pool=0
                    Task#327 step 1/2 (+0s): 7.645µs self time
                    Task#327 step 2/2 (+7.645µs): return nil
                    Task#327 ends at 7.645µs
                      Gather#327: index=1
                      Gather#327 step 1/8 (+0s): 32.137µs self time
                      Gather#327 step 2/8 (+32.137µs): scatter:
                        Task#321: pool=1
                        Task#321 step 1/2 (+0s): 9.999µs self time
                        Task#321 step 2/2 (+9.999µs): return nil
                        Task#321 ends at 49.781µs
                          Gather#321: index=0
                          Gather#321 step 1/4 (+0s): 7.589µs self time
                          Gather#321 step 2/4 (+7.589µs): scatter:
                            Task#319: pool=1
                            Task#319 step 1/2 (+0s): 9.998µs self time
                            Task#319 step 2/2 (+9.998µs): return nil
                            Task#319 ends at 67.368µs
                              Combine#319: index=10 flush=<nil>
                              Combine#319 step 1/4 (+0s): 78.936µs self time
                              Combine#319 step 2/4 (+78.936µs): scatter:
                                Task#279: pool=0
                                Task#279 step 1/2 (+0s): 0s self time
                                Task#279 step 2/2 (+0s): return nil
                                Task#279 ends at 146.304µs
                                  Gather#279: index=0
                                  Gather#279 step 1/2 (+0s): 19.554µs self time
                                  Gather#279 step 2/2 (+19.554µs): return nil
                                  Gather#279 ends at 165.858µs
                              Combine#319 step 3/4 (+78.936µs): 27.99µs self time
                              Combine#319 step 4/4 (+106.926µs): return nil
                              Combine#319 ends at 174.294µs
                          Gather#321 step 3/4 (+7.589µs): 7.601µs self time
                          Gather#321 step 4/4 (+15.19µs): return nil
                          Gather#321 ends at 64.971µs
                      Gather#327 step 3/8 (+32.137µs): 32.182µs self time
                      Gather#327 step 4/8 (+64.319µs): scatter:
                        Task#322: pool=1
                        Task#322 step 1/2 (+0s): 10.003µs self time
                        Task#322 step 2/2 (+10.003µs): return nil
                        Task#322 ends at 81.967µs
                          Gather#322: index=0
                          Gather#322 step 1/4 (+0s): 0s self time
                          Gather#322 step 2/4 (+0s): scatter:
                            Task#280: pool=1
                            Task#280 step 1/2 (+0s): 9.998µs self time
                            Task#280 step 2/2 (+9.998µs): return nil
                            Task#280 ends at 91.965µs
                              Gather#280: index=1
                              Gather#280 step 1/2 (+0s): 985ns self time
                              Gather#280 step 2/2 (+985ns): return nil
                              Gather#280 ends at 92.95µs
                          Gather#322 step 3/4 (+0s): 872ns self time
                          Gather#322 step 4/4 (+872ns): return nil
                          Gather#322 ends at 82.839µs
                      Gather#327 step 5/8 (+64.319µs): 32.185µs self time
                      Gather#327 step 6/8 (+96.504µs): scatter:
                        Task#323: pool=0
                        Task#323 step 1/2 (+0s): 8.685µs self time
                        Task#323 step 2/2 (+8.685µs): return nil
                        Task#323 ends at 112.834µs
                          Combine#323: index=7 flush=<nil>
                          Combine#323 step 1/6 (+0s): 446ns self time
                          Combine#323 step 2/6 (+446ns): scatter:
                            Task#276: pool=0
                            Task#276 step 1/2 (+0s): 10.937µs self time
                            Task#276 step 2/2 (+10.937µs): return nil
                            Task#276 ends at 124.217µs
                              Gather#276: index=1
                              Gather#276 step 1/2 (+0s): 899ns self time
                              Gather#276 step 2/2 (+899ns): return nil
                              Gather#276 ends at 125.116µs
                          Combine#323 step 3/6 (+446ns): 443ns self time
                          Combine#323 step 4/6 (+889ns): scatter:
                            Task#320: pool=1
                            Task#320 step 1/2 (+0s): 9.997µs self time
                            Task#320 step 2/2 (+9.997µs): return nil
                            Task#320 ends at 123.72µs
                              Gather#320: index=0
                              Gather#320 step 1/6 (+0s): 332ns self time
                              Gather#320 step 2/6 (+332ns): scatter:
                                Task#318: pool=0
                                Task#318 step 1/2 (+0s): 9.995µs self time
                                Task#318 step 2/2 (+9.995µs): return nil
                                Task#318 ends at 134.047µs
                                  Combine#318: index=10 flush=<nil>
                                  Combine#318 step 1/4 (+0s): 1.591µs self time
                                  Combine#318 step 2/4 (+1.591µs): scatter:
                                    Task#313: pool=1
                                    Task#313 step 1/2 (+0s): 4.293257ms self time
                                    Task#313 step 2/2 (+4.293257ms): return nil
                                    Task#313 ends at 4.428895ms
                                      Gather#313: index=0
                                      Gather#313 step 1/2 (+0s): 1.003µs self time
                                      Gather#313 step 2/2 (+1.003µs): return nil
                                      Gather#313 ends at 4.429898ms
                                  Combine#318 step 3/4 (+1.591µs): 12.145µs self time
                                  Combine#318 step 4/4 (+13.736µs): return nil
                                  Combine#318 ends at 147.783µs
                              Gather#320 step 3/6 (+332ns): 336ns self time
                              Gather#320 step 4/6 (+668ns): scatter:
                                Task#317: pool=0
                                Task#317 step 1/2 (+0s): 9.985µs self time
                                Task#317 step 2/2 (+9.985µs): return error
                                Task#317 ends at 134.373µs
                                  Gather#317: index=1
                                  Gather#317 step 1/12 (+0s): 140ns self time
                                  Gather#317 step 2/12 (+140ns): scatter:
                                    Task#316: pool=1
                                    Task#316 step 1/2 (+0s): 9.974µs self time
                                    Task#316 step 2/2 (+9.974µs): return nil
                                    Task#316 ends at 144.487µs
                                      Gather#316: index=1
                                      Gather#316 step 1/4 (+0s): 708ns self time
                                      Gather#316 step 2/4 (+708ns): scatter:
                                        Task#284: pool=0
                                        Task#284 step 1/2 (+0s): 12.038µs self time
                                        Task#284 step 2/2 (+12.038µs): return nil
                                        Task#284 ends at 157.233µs
                                          Gather#284: index=0
                                          Gather#284 step 1/2 (+0s): 1.001µs self time
                                          Gather#284 step 2/2 (+1.001µs): return nil
                                          Gather#284 ends at 158.234µs
                                      Gather#316 step 3/4 (+708ns): 295ns self time
                                      Gather#316 step 4/4 (+1.003µs): return error
                                      Gather#316 ends at 145.49µs
                                  Gather#317 step 3/12 (+140ns): 360ns self time
                                  Gather#317 step 4/12 (+500ns): scatter:
                                    Task#315: pool=1
                                    Task#315 step 1/2 (+0s): 10.056µs self time
                                    Task#315 step 2/2 (+10.056µs): return nil
                                    Task#315 ends at 144.929µs
                                      Gather#315: index=1
                                      Gather#315 step 1/8 (+0s): 31ns self time
                                      Gather#315 step 2/8 (+31ns): scatter:
                                        Task#275: pool=0
                                        Task#275 step 1/2 (+0s): 9.004µs self time
                                        Task#275 step 2/2 (+9.004µs): return nil
                                        Task#275 ends at 153.964µs
                                          Gather#275: index=0
                                          Gather#275 step 1/2 (+0s): 983ns self time
                                          Gather#275 step 2/2 (+983ns): return error
                                          Gather#275 ends at 154.947µs
                                      Gather#315 step 3/8 (+31ns): 686ns self time
                                      Gather#315 step 4/8 (+717ns): scatter:
                                        Task#274: pool=1
                                        Task#274 step 1/2 (+0s): 9.989µs self time
                                        Task#274 step 2/2 (+9.989µs): return nil
                                        Task#274 ends at 155.635µs
                                          Gather#274: index=1
                                          Gather#274 step 1/2 (+0s): 362ns self time
                                          Gather#274 step 2/2 (+362ns): return nil
                                          Gather#274 ends at 155.997µs
                                      Gather#315 step 5/8 (+717ns): 32ns self time
                                      Gather#315 step 6/8 (+749ns): scatter:
                                        Task#285: pool=0
                                        Task#285 step 1/2 (+0s): 9.999µs self time
                                        Task#285 step 2/2 (+9.999µs): return nil
                                        Task#285 ends at 155.677µs
                                          Gather#285: index=0
                                          Gather#285 step 1/4 (+0s): 23.217µs self time
                                          Gather#285 step 2/4 (+23.217µs): subjob:
                                            Plan#13: pathCount=15 taskCount=27 maxPathDuration=10.043056ms minGatherCount=18 maxGatherCount=95
                                               TaskPools[0]: TaskPool#62: limit=3
                                               TaskPools[1]: TaskPool#63: limit=10
                                               TaskPools[2]: TaskPool#64: limit=4
                                               CombinerPools[0]: CombinerPool#55: limit=4
                                               CombinerPools[1]: CombinerPool#56: limit=1
                                               CombinerPools[2]: CombinerPool#57: limit=9
                                               CombinerPools[3]: CombinerPool#58: limit=7
                                               Combiners[0]: pool=3
                                               Combiners[1]: pool=2
                                               Combiners[2]: pool=2
                                            Plan#13 step 1/4 (+0s): scatter:
                                              Task#311: pool=2
                                              Task#311 step 1/2 (+0s): 9.997µs self time
                                              Task#311 step 2/2 (+9.997µs): return nil
                                              Task#311 ends at 9.997µs
                                                Gather#311: index=4
                                                Gather#311 step 1/6 (+0s): 369ns self time
                                                Gather#311 step 2/6 (+369ns): scatter:
                                                  Task#287: pool=0
                                                  Task#287 step 1/2 (+0s): 9.996µs self time
                                                  Task#287 step 2/2 (+9.996µs): return nil
                                                  Task#287 ends at 20.362µs
                                                    Gather#287: index=3
                                                    Gather#287 step 1/2 (+0s): 1.001µs self time
                                                    Gather#287 step 2/2 (+1.001µs): return nil
                                                    Gather#287 ends at 21.363µs
                                                Gather#311 step 3/6 (+369ns): 272ns self time
                                                Gather#311 step 4/6 (+641ns): scatter:
                                                  Task#309: pool=1
                                                  Task#309 step 1/2 (+0s): 10.061µs self time
                                                  Task#309 step 2/2 (+10.061µs): return nil
                                                  Task#309 ends at 20.699µs
                                                    Gather#309: index=7
                                                    Gather#309 step 1/4 (+0s): 83ns self time
                                                    Gather#309 step 2/4 (+83ns): scatter:
                                                      Task#305: pool=2
                                                      Task#305 step 1/2 (+0s): 0s self time
                                                      Task#305 step 2/2 (+0s): return nil
                                                      Task#305 ends at 20.782µs
                                                        Gather#305: index=1
                                                        Gather#305 step 1/4 (+0s): 254ns self time
                                                        Gather#305 step 2/4 (+254ns): scatter:
                                                          Task#290: pool=1
                                                          Task#290 step 1/2 (+0s): 9.969µs self time
                                                          Task#290 step 2/2 (+9.969µs): return nil
                                                          Task#290 ends at 31.005µs
                                                            Combine#290: index=1 flush=<nil>
                                                            Combine#290 step 1/2 (+0s): 1ns self time
                                                            Combine#290 step 2/2 (+1ns): return nil
                                                            Combine#290 ends at 31.006µs
                                                        Gather#305 step 3/4 (+254ns): 259ns self time
                                                        Gather#305 step 4/4 (+513ns): return nil
                                                        Gather#305 ends at 21.295µs
                                                    Gather#309 step 3/4 (+83ns): 94ns self time
                                                    Gather#309 step 4/4 (+177ns): return nil
                                                    Gather#309 ends at 20.876µs
                                                Gather#311 step 5/6 (+641ns): 344ns self time
                                                Gather#311 step 6/6 (+985ns): return nil
                                                Gather#311 ends at 10.982µs
                                            Plan#13 step 2/4 (+0s): scatter:
                                              Task#312: pool=0
                                              Task#312 step 1/2 (+0s): 6.146µs self time
                                              Task#312 step 2/2 (+6.146µs): return nil
                                              Task#312 ends at 6.146µs
                                                Gather#312: index=7
                                                Gather#312 step 1/4 (+0s): 492ns self time
                                                Gather#312 step 2/4 (+492ns): scatter:
                                                  Task#294: pool=2
                                                  Task#294 step 1/2 (+0s): 9.987µs self time
                                                  Task#294 step 2/2 (+9.987µs): return error
                                                  Task#294 ends at 16.625µs
                                                    Gather#294: index=6
                                                    Gather#294 step 1/2 (+0s): 156ns self time
                                                    Gather#294 step 2/2 (+156ns): return nil
                                                    Gather#294 ends at 16.781µs
                                                Gather#312 step 3/4 (+492ns): 498ns self time
                                                Gather#312 step 4/4 (+990ns): return nil
                                                Gather#312 ends at 7.136µs
                                            Plan#13 step 3/4 (+0s): scatter:
                                              Task#310: pool=1
                                              Task#310 step 1/2 (+0s): 10.02µs self time
                                              Task#310 step 2/2 (+10.02µs): return nil
                                              Task#310 ends at 10.02µs
                                                Gather#310: index=3
                                                Gather#310 step 1/8 (+0s): 454ns self time
                                                Gather#310 step 2/8 (+454ns): scatter:
                                                  Task#308: pool=2
                                                  Task#308 step 1/2 (+0s): 10.079µs self time
                                                  Task#308 step 2/2 (+10.079µs): return nil
                                                  Task#308 ends at 20.553µs
                                                    Combine#308: index=1 flush=<nil>
                                                    Combine#308 step 1/12 (+0s): 153ns self time
                                                    Combine#308 step 2/12 (+153ns): scatter:
                                                      Task#293: pool=1
                                                      Task#293 step 1/2 (+0s): 7.153212ms self time
                                                      Task#293 step 2/2 (+7.153212ms): return nil
                                                      Task#293 ends at 7.173918ms
                                                        Gather#293: index=1
                                                        Gather#293 step 1/2 (+0s): 2.803µs self time
                                                        Gather#293 step 2/2 (+2.803µs): return nil
                                                        Gather#293 ends at 7.176721ms
                                                    Combine#308 step 3/12 (+153ns): 157ns self time
                                                    Combine#308 step 4/12 (+310ns): scatter:
                                                      Task#306: pool=1
                                                      Task#306 step 1/2 (+0s): 5.228µs self time
                                                      Task#306 step 2/2 (+5.228µs): return nil
                                                      Task#306 ends at 26.091µs
                                                        Combine#306: index=1 flush=<nil>
                                                        Combine#306 step 1/6 (+0s): 234ns self time
                                                        Combine#306 step 2/6 (+234ns): scatter:
                                                          Task#288: pool=2
                                                          Task#288 step 1/2 (+0s): 10.312µs self time
                                                          Task#288 step 2/2 (+10.312µs): return nil
                                                          Task#288 ends at 36.637µs
                                                            Gather#288: index=6
                                                            Gather#288 step 1/2 (+0s): 996ns self time
                                                            Gather#288 step 2/2 (+996ns): return nil
                                                            Gather#288 ends at 37.633µs
                                                        Combine#306 step 3/6 (+234ns): 1.298µs self time
                                                        Combine#306 step 4/6 (+1.532µs): scatter:
                                                          Task#286: pool=2
                                                          Task#286 step 1/2 (+0s): 472.047µs self time
                                                          Task#286 step 2/2 (+472.047µs): return nil
                                                          Task#286 ends at 499.67µs
                                                            Combine#286: index=1 flush=<nil>
                                                            Combine#286 step 1/2 (+0s): 996ns self time
                                                            Combine#286 step 2/2 (+996ns): return nil
                                                            Combine#286 ends at 500.666µs
                                                        Combine#306 step 5/6 (+1.532µs): 699ns self time
                                                        Combine#306 step 6/6 (+2.231µs): return nil
                                                        Combine#306 ends at 28.322µs
                                                    Combine#308 step 5/12 (+310ns): 163ns self time
                                                    Combine#308 step 6/12 (+473ns): scatter:
                                                      Task#299: pool=2
                                                      Task#299 step 1/2 (+0s): 10.028µs self time
                                                      Task#299 step 2/2 (+10.028µs): return nil
                                                      Task#299 ends at 31.054µs
                                                        Combine#299: index=2 flush=<nil>
                                                        Combine#299 step 1/2 (+0s): 1ms self time
                                                        Combine#299 step 2/2 (+1ms): return error
                                                        Combine#299 ends at 1.031054ms
                                                    Combine#308 step 7/12 (+473ns): 374ns self time
                                                    Combine#308 step 8/12 (+847ns): scatter:
                                                      Task#307: pool=0
                                                      Task#307 step 1/2 (+0s): 9.819µs self time
                                                      Task#307 step 2/2 (+9.819µs): return nil
                                                      Task#307 ends at 31.219µs
                                                        Combine#307: index=0 flush=<nil>
                                                        Combine#307 step 1/4 (+0s): 503ns self time
                                                        Combine#307 step 2/4 (+503ns): scatter:
                                                          Task#300: pool=1
                                                          Task#300 step 1/2 (+0s): 10.025µs self time
                                                          Task#300 step 2/2 (+10.025µs): return nil
                                                          Task#300 ends at 41.747µs
                                                            Combine#300: index=0 flush=<nil>
                                                            Combine#300 step 1/2 (+0s): 999ns self time
                                                            Combine#300 step 2/2 (+999ns): return nil
                                                            Combine#300 ends at 42.746µs
                                                        Combine#307 step 3/4 (+503ns): 495ns self time
                                                        Combine#307 step 4/4 (+998ns): return nil
                                                        Combine#307 ends at 32.217µs
                                                    Combine#308 step 9/12 (+847ns): 87ns self time
                                                    Combine#308 step 10/12 (+934ns): scatter:
                                                      Task#304: pool=2
                                                      Task#304 step 1/2 (+0s): 9.984µs self time
                                                      Task#304 step 2/2 (+9.984µs): return nil
                                                      Task#304 ends at 31.471µs
                                                        Gather#304: index=8
                                                        Gather#304 step 1/10 (+0s): 162ns self time
                                                        Gather#304 step 2/10 (+162ns): scatter:
                                                          Task#302: pool=1
                                                          Task#302 step 1/2 (+0s): 10ms self time
                                                          Task#302 step 2/2 (+10ms): return nil
                                                          Task#302 ends at 10.031633ms
                                                            Gather#302: index=4
                                                            Gather#302 step 1/4 (+0s): 423ns self time
                                                            Gather#302 step 2/4 (+423ns): scatter:
                                                              Task#297: pool=1
                                                              Task#297 step 1/2 (+0s): 10.001µs self time
                                                              Task#297 step 2/2 (+10.001µs): return nil
                                                              Task#297 ends at 10.042057ms
                                                                Combine#297: index=2 flush=<nil>
                                                                Combine#297 step 1/2 (+0s): 999ns self time
                                                                Combine#297 step 2/2 (+999ns): return nil
                                                                Combine#297 ends at 10.043056ms
                                                            Gather#302 step 3/4 (+423ns): 577ns self time
                                                            Gather#302 step 4/4 (+1µs): return nil
                                                            Gather#302 ends at 10.032633ms
                                                        Gather#304 step 3/10 (+162ns): 428ns self time
                                                        Gather#304 step 4/10 (+590ns): scatter:
                                                          Task#303: pool=1
                                                          Task#303 step 1/2 (+0s): 9.997µs self time
                                                          Task#303 step 2/2 (+9.997µs): return nil
                                                          Task#303 ends at 42.058µs
                                                            Gather#303: index=2
                                                            Gather#303 step 1/4 (+0s): 346ns self time
                                                            Gather#303 step 2/4 (+346ns): scatter:
                                                              Task#301: pool=0
                                                              Task#301 step 1/2 (+0s): 10µs self time
                                                              Task#301 step 2/2 (+10µs): return nil
                                                              Task#301 ends at 52.404µs
                                                                Gather#301: index=0
                                                                Gather#301 step 1/6 (+0s): 329ns self time
                                                                Gather#301 step 2/6 (+329ns): scatter:
                                                                  Task#298: pool=2
                                                                  Task#298 step 1/2 (+0s): 9.984µs self time
                                                                  Task#298 step 2/2 (+9.984µs): return nil
                                                                  Task#298 ends at 62.717µs
                                                                    Gather#298: index=8
                                                                    Gather#298 step 1/2 (+0s): 1.437µs self time
                                                                    Gather#298 step 2/2 (+1.437µs): return nil
                                                                    Gather#298 ends at 64.154µs
                                                                Gather#301 step 3/6 (+329ns): 0s self time
                                                                Gather#301 step 4/6 (+329ns): scatter:
                                                                  Task#296: pool=0
                                                                  Task#296 step 1/2 (+0s): 10.061µs self time
                                                                  Task#296 step 2/2 (+10.061µs): return nil
                                                                  Task#296 ends at 62.794µs
                                                                    Gather#296: index=0
                                                                    Gather#296 step 1/2 (+0s): 996ns self time
                                                                    Gather#296 step 2/2 (+996ns): return nil
                                                                    Gather#296 ends at 63.79µs
                                                                Gather#301 step 5/6 (+329ns): 650ns self time
                                                                Gather#301 step 6/6 (+979ns): return nil
                                                                Gather#301 ends at 53.383µs
                                                            Gather#303 step 3/4 (+346ns): 653ns self time
                                                            Gather#303 step 4/4 (+999ns): return nil
                                                            Gather#303 ends at 43.057µs
                                                        Gather#304 step 5/10 (+590ns): 80ns self time
                                                        Gather#304 step 6/10 (+670ns): scatter:
                                                          Task#289: pool=1
                                                          Task#289 step 1/2 (+0s): 10µs self time
                                                          Task#289 step 2/2 (+10µs): return nil
                                                          Task#289 ends at 42.141µs
                                                            Combine#289: index=1 flush=<nil>
                                                            Combine#289 step 1/2 (+0s): 999ns self time
                                                            Combine#289 step 2/2 (+999ns): return nil
                                                            Combine#289 ends at 43.14µs
                                                        Gather#304 step 7/10 (+670ns): 0s self time
                                                        Gather#304 step 8/10 (+670ns): scatter:
                                                          Task#291: pool=0
                                                          Task#291 step 1/2 (+0s): 75.258µs self time
                                                          Task#291 step 2/2 (+75.258µs): return nil
                                                          Task#291 ends at 107.399µs
                                                            Gather#291: index=2
                                                            Gather#291 step 1/2 (+0s): 984ns self time
                                                            Gather#291 step 2/2 (+984ns): return nil
                                                            Gather#291 ends at 108.383µs
                                                        Gather#304 step 9/10 (+670ns): 143ns self time
                                                        Gather#304 step 10/10 (+813ns): return nil
                                                        Gather#304 ends at 32.284µs
                                                    Combine#308 step 11/12 (+934ns): 73ns self time
                                                    Combine#308 step 12/12 (+1.007µs): return nil
                                                    Combine#308 ends at 21.56µs
                                                Gather#310 step 3/8 (+454ns): 47ns self time
                                                Gather#310 step 4/8 (+501ns): scatter:
                                                  Task#295: pool=1
                                                  Task#295 step 1/2 (+0s): 10.91µs self time
                                                  Task#295 step 2/2 (+10.91µs): return nil
                                                  Task#295 ends at 21.431µs
                                                    Gather#295: index=6
                                                    Gather#295 step 1/2 (+0s): 696ns self time
                                                    Gather#295 step 2/2 (+696ns): return nil
                                                    Gather#295 ends at 22.127µs
                                                Gather#310 step 5/8 (+501ns): 84ns self time
                                                Gather#310 step 6/8 (+585ns): scatter:
                                                  Task#292: pool=2
                                                  Task#292 step 1/2 (+0s): 12.021µs self time
                                                  Task#292 step 2/2 (+12.021µs): return nil
                                                  Task#292 ends at 22.626µs
                                                    Gather#292: index=9
                                                    Gather#292 step 1/2 (+0s): 1ms self time
                                                    Gather#292 step 2/2 (+1ms): return nil
                                                    Gather#292 ends at 1.022626ms
                                                Gather#310 step 7/8 (+585ns): 29ns self time
                                                Gather#310 step 8/8 (+614ns): return error
                                                Gather#310 ends at 10.634µs
                                            Plan#13 step 4/4 (+0s): ends at 10.043056ms
                                          Gather#285 step 3/4 (+10.066273ms): 23.217µs self time
                                          Gather#285 step 4/4 (+10.08949ms): return nil
                                          Gather#285 ends at 10.245167ms
                                      Gather#315 step 7/8 (+749ns): 252ns self time
                                      Gather#315 step 8/8 (+1.001µs): return nil
                                      Gather#315 ends at 145.93µs
                                  Gather#317 step 5/12 (+500ns): 126ns self time
                                  Gather#317 step 6/12 (+626ns): scatter:
                                    Task#277: pool=0
                                    Task#277 step 1/2 (+0s): 7.856µs self time
                                    Task#277 step 2/2 (+7.856µs): return nil
                                    Task#277 ends at 142.855µs
                                      Gather#277: index=1
                                      Gather#277 step 1/2 (+0s): 744ns self time
                                      Gather#277 step 2/2 (+744ns): return nil
                                      Gather#277 ends at 143.599µs
                                  Gather#317 step 7/12 (+626ns): 176ns self time
                                  Gather#317 step 8/12 (+802ns): scatter:
                                    Task#278: pool=0
                                    Task#278 step 1/2 (+0s): 3.038297ms self time
                                    Task#278 step 2/2 (+3.038297ms): return error
                                    Task#278 ends at 3.173472ms
                                      Gather#278: index=0
                                      Gather#278 step 1/2 (+0s): 999ns self time
                                      Gather#278 step 2/2 (+999ns): return nil
                                      Gather#278 ends at 3.174471ms
                                  Gather#317 step 9/12 (+802ns): 189ns self time
                                  Gather#317 step 10/12 (+991ns): scatter:
                                    Task#283: pool=1
                                    Task#283 step 1/2 (+0s): 10ms self time
                                    Task#283 step 2/2 (+10ms): return nil
                                    Task#283 ends at 10.135364ms
                                      Combine#283: index=1 flush=<nil>
                                      Combine#283 step 1/2 (+0s): 999ns self time
                                      Combine#283 step 2/2 (+999ns): return nil
                                      Combine#283 ends at 10.136363ms
                                  Gather#317 step 11/12 (+991ns): 5ns self time
                                  Gather#317 step 12/12 (+996ns): return nil
                                  Gather#317 ends at 135.369µs
                              Gather#320 step 5/6 (+668ns): 335ns self time
                              Gather#320 step 6/6 (+1.003µs): return nil
                              Gather#320 ends at 124.723µs
                          Combine#323 step 5/6 (+889ns): 443ns self time
                          Combine#323 step 6/6 (+1.332µs): return nil
                          Combine#323 ends at 114.166µs
                      Gather#327 step 7/8 (+96.504µs): 32.173µs self time
                      Gather#327 step 8/8 (+128.677µs): return nil
                      Gather#327 ends at 136.322µs
                  Plan#12 step 4/4 (+0s): ends at 10.245167ms
                Task#273 step 3/4 (+10.249962ms): 4.788µs self time
                Task#273 step 4/4 (+10.25475ms): return error
                Task#273 ends at 10.264887ms
                  Gather#273: index=3
                  Gather#273 step 1/12 (+0s): 641ns self time
                  Gather#273 step 2/12 (+641ns): scatter:
                    Task#271: pool=2
                    Task#271 step 1/2 (+0s): 13.931µs self time
                    Task#271 step 2/2 (+13.931µs): return nil
                    Task#271 ends at 10.279459ms
                      Gather#271: index=1
                      Gather#271 step 1/8 (+0s): 801ns self time
                      Gather#271 step 2/8 (+801ns): scatter:
                        Task#5: pool=3
                        Task#5 step 1/2 (+0s): 0s self time
                        Task#5 step 2/2 (+0s): return nil
                        Task#5 ends at 10.28026ms
                          Gather#5: index=0
                          Gather#5 step 1/2 (+0s): 1µs self time
                          Gather#5 step 2/2 (+1µs): return nil
                          Gather#5 ends at 10.28126ms
                      Gather#271 step 3/8 (+801ns): 63ns self time
                      Gather#271 step 4/8 (+864ns): scatter:
                        Task#129: pool=3
                        Task#129 step 1/2 (+0s): 9.998µs self time
                        Task#129 step 2/2 (+9.998µs): return nil
                        Task#129 ends at 10.290321ms
                          Combine#129: index=3 flush=<nil>
                          Combine#129 step 1/2 (+0s): 1.769µs self time
                          Combine#129 step 2/2 (+1.769µs): return nil
                          Combine#129 ends at 10.29209ms
                      Gather#271 step 5/8 (+864ns): 88ns self time
                      Gather#271 step 6/8 (+952ns): scatter:
                        Task#2: pool=9
                        Task#2 step 1/2 (+0s): 10.014µs self time
                        Task#2 step 2/2 (+10.014µs): return nil
                        Task#2 ends at 10.290425ms
                          Combine#2: index=1 flush=<nil>
                          Combine#2 step 1/2 (+0s): 438ns self time
                          Combine#2 step 2/2 (+438ns): return nil
                          Combine#2 ends at 10.290863ms
                      Gather#271 step 7/8 (+952ns): 51ns self time
                      Gather#271 step 8/8 (+1.003µs): return nil
                      Gather#271 ends at 10.280462ms
                  Gather#273 step 3/12 (+641ns): 70ns self time
                  Gather#273 step 4/12 (+711ns): scatter:
                    Task#128: pool=2
                    Task#128 step 1/2 (+0s): 9.988µs self time
                    Task#128 step 2/2 (+9.988µs): return nil
                    Task#128 ends at 10.275586ms
                      Combine#128: index=1 flush=<nil>
                      Combine#128 step 1/2 (+0s): 27.052µs self time
                      Combine#128 step 2/2 (+27.052µs): return nil
                      Combine#128 ends at 10.302638ms
                  Gather#273 step 5/12 (+711ns): 6ns self time
                  Gather#273 step 6/12 (+717ns): scatter:
                    Task#272: pool=2
                    Task#272 step 1/2 (+0s): 10.055µs self time
                    Task#272 step 2/2 (+10.055µs): return nil
                    Task#272 ends at 10.275659ms
                      Gather#272: index=4
                      Gather#272 step 1/6 (+0s): 300ns self time
                      Gather#272 step 2/6 (+300ns): scatter:
                        Task#269: pool=8
                        Task#269 step 1/2 (+0s): 9.996µs self time
                        Task#269 step 2/2 (+9.996µs): return nil
                        Task#269 ends at 10.285955ms
                          Gather#269: index=0
                          Gather#269 step 1/6 (+0s): 249ns self time
                          Gather#269 step 2/6 (+249ns): scatter:
                            Task#132: pool=1
                            Task#132 step 1/4 (+0s): 5.061µs self time
                            Task#132 step 2/4 (+5.061µs): subjob:
                              Plan#7: pathCount=28 taskCount=47 maxPathDuration=9.549022ms minGatherCount=29 maxGatherCount=114
                                 TaskPools[0]: TaskPool#36: limit=10
                                 TaskPools[1]: TaskPool#37: limit=5
                                 TaskPools[2]: TaskPool#38: limit=2
                                 TaskPools[3]: TaskPool#39: limit=1
                                 TaskPools[4]: TaskPool#40: limit=10
                                 TaskPools[5]: TaskPool#41: limit=1
                                 TaskPools[6]: TaskPool#42: limit=5
                                 TaskPools[7]: TaskPool#43: limit=2
                                 TaskPools[8]: TaskPool#44: limit=1
                                 TaskPools[9]: TaskPool#45: limit=6
                                 CombinerPools[0]: CombinerPool#28: limit=4
                                 CombinerPools[1]: CombinerPool#29: limit=5
                                 CombinerPools[2]: CombinerPool#30: limit=1
                                 Combiners[0]: pool=1
                                 Combiners[1]: pool=1
                                 Combiners[2]: pool=0
                                 Combiners[3]: pool=1
                                 Combiners[4]: pool=2
                                 Combiners[5]: pool=1
                                 Combiners[6]: pool=1
                                 Combiners[7]: pool=0
                                 Combiners[8]: pool=1
                                 Combiners[9]: pool=1
                                 Combiners[10]: pool=2
                                 Combiners[11]: pool=1
                              Plan#7 step 1/6 (+0s): scatter:
                                Task#194: pool=2
                                Task#194 step 1/2 (+0s): 9.997µs self time
                                Task#194 step 2/2 (+9.997µs): return nil
                                Task#194 ends at 9.997µs
                                  Combine#194: index=11 flush=<nil>
                                  Combine#194 step 1/14 (+0s): 486ns self time
                                  Combine#194 step 2/14 (+486ns): scatter:
                                    Task#189: pool=6
                                    Task#189 step 1/2 (+0s): 10.763µs self time
                                    Task#189 step 2/2 (+10.763µs): return nil
                                    Task#189 ends at 21.246µs
                                      Combine#189: index=2 flush=<nil>
                                      Combine#189 step 1/4 (+0s): 35.547µs self time
                                      Combine#189 step 2/4 (+35.547µs): scatter:
                                        Task#187: pool=4
                                        Task#187 step 1/2 (+0s): 10.029µs self time
                                        Task#187 step 2/2 (+10.029µs): return nil
                                        Task#187 ends at 66.822µs
                                          Combine#187: index=6 flush=<nil>
                                          Combine#187 step 1/4 (+0s): 517ns self time
                                          Combine#187 step 2/4 (+517ns): scatter:
                                            Task#182: pool=3
                                            Task#182 step 1/2 (+0s): 10.203µs self time
                                            Task#182 step 2/2 (+10.203µs): return nil
                                            Task#182 ends at 77.542µs
                                              Combine#182: index=6 flush=<nil>
                                              Combine#182 step 1/8 (+0s): 378.928µs self time
                                              Combine#182 step 2/8 (+378.928µs): scatter:
                                                Task#180: pool=1
                                                Task#180 step 1/2 (+0s): 13.773µs self time
                                                Task#180 step 2/2 (+13.773µs): return nil
                                                Task#180 ends at 470.243µs
                                                  Combine#180: index=5 flush=<nil>
                                                  Combine#180 step 1/8 (+0s): 55ns self time
                                                  Combine#180 step 2/8 (+55ns): scatter:
                                                    Task#171: pool=2
                                                    Task#171 step 1/2 (+0s): 9.999µs self time
                                                    Task#171 step 2/2 (+9.999µs): return nil
                                                    Task#171 ends at 480.297µs
                                                      Gather#171: index=0
                                                      Gather#171 step 1/2 (+0s): 998ns self time
                                                      Gather#171 step 2/2 (+998ns): return nil
                                                      Gather#171 ends at 481.295µs
                                                  Combine#180 step 3/8 (+55ns): 0s self time
                                                  Combine#180 step 4/8 (+55ns): scatter:
                                                    Task#179: pool=4
                                                    Task#179 step 1/2 (+0s): 10.002µs self time
                                                    Task#179 step 2/2 (+10.002µs): return nil
                                                    Task#179 ends at 480.3µs
                                                      Gather#179: index=0
                                                      Gather#179 step 1/2 (+0s): 1.076µs self time
                                                      Gather#179 step 2/2 (+1.076µs): return nil
                                                      Gather#179 ends at 481.376µs
                                                  Combine#180 step 5/8 (+55ns): 47ns self time
                                                  Combine#180 step 6/8 (+102ns): scatter:
                                                    Task#149: pool=7
                                                    Task#149 step 1/2 (+0s): 5.690402ms self time
                                                    Task#149 step 2/2 (+5.690402ms): return nil
                                                    Task#149 ends at 6.160747ms
                                                      Gather#149: index=0
                                                      Gather#149 step 1/4 (+0s): 703ns self time
                                                      Gather#149 step 2/4 (+703ns): subjob:
                                                        Plan#8: pathCount=10 taskCount=19 maxPathDuration=3.38744ms minGatherCount=16 maxGatherCount=21
                                                           TaskPools[0]: TaskPool#46: limit=1
                                                           CombinerPools[0]: CombinerPool#31: limit=1
                                                           CombinerPools[1]: CombinerPool#32: limit=2
                                                           CombinerPools[2]: CombinerPool#33: limit=2
                                                           CombinerPools[3]: CombinerPool#34: limit=2
                                                           CombinerPools[4]: CombinerPool#35: limit=5
                                                           CombinerPools[5]: CombinerPool#36: limit=1
                                                           CombinerPools[6]: CombinerPool#37: limit=1
                                                           CombinerPools[7]: CombinerPool#38: limit=1
                                                           CombinerPools[8]: CombinerPool#39: limit=1
                                                           CombinerPools[9]: CombinerPool#40: limit=1
                                                           Combiners[0]: pool=6
                                                           Combiners[1]: pool=2
                                                           Combiners[2]: pool=4
                                                           Combiners[3]: pool=1
                                                           Combiners[4]: pool=3
                                                           Combiners[5]: pool=1
                                                           Combiners[6]: pool=1
                                                           Combiners[7]: pool=0
                                                           Combiners[8]: pool=1
                                                           Combiners[9]: pool=5
                                                           Combiners[10]: pool=1
                                                           Combiners[11]: pool=3
                                                           Combiners[12]: pool=7
                                                           Combiners[13]: pool=3
                                                           Combiners[14]: pool=5
                                                           Combiners[15]: pool=0
                                                        Plan#8 step 1/3 (+0s): scatter:
                                                          Task#167: pool=0
                                                          Task#167 step 1/2 (+0s): 10.12µs self time
                                                          Task#167 step 2/2 (+10.12µs): return nil
                                                          Task#167 ends at 10.12µs
                                                            Gather#167: index=2
                                                            Gather#167 step 1/12 (+0s): 173ns self time
                                                            Gather#167 step 2/12 (+173ns): scatter:
                                                              Task#165: pool=0
                                                              Task#165 step 1/2 (+0s): 2.332µs self time
                                                              Task#165 step 2/2 (+2.332µs): return nil
                                                              Task#165 ends at 12.625µs
                                                                Gather#165: index=2
                                                                Gather#165 step 1/4 (+0s): 489ns self time
                                                                Gather#165 step 2/4 (+489ns): scatter:
                                                                  Task#162: pool=0
                                                                  Task#162 step 1/2 (+0s): 3.237µs self time
                                                                  Task#162 step 2/2 (+3.237µs): return nil
                                                                  Task#162 ends at 16.351µs
                                                                    Gather#162: index=2
                                                                    Gather#162 step 1/4 (+0s): 6.266µs self time
                                                                    Gather#162 step 2/4 (+6.266µs): scatter:
                                                                      Task#160: pool=0
                                                                      Task#160 step 1/2 (+0s): 10µs self time
                                                                      Task#160 step 2/2 (+10µs): return nil
                                                                      Task#160 ends at 32.617µs
                                                                        Gather#160: index=2
                                                                        Gather#160 step 1/6 (+0s): 618ns self time
                                                                        Gather#160 step 2/6 (+618ns): scatter:
                                                                          Task#157: pool=0
                                                                          Task#157 step 1/2 (+0s): 5.317µs self time
                                                                          Task#157 step 2/2 (+5.317µs): return error
                                                                          Task#157 ends at 38.552µs
                                                                            Gather#157: index=4
                                                                            Gather#157 step 1/2 (+0s): 8.306µs self time
                                                                            Gather#157 step 2/2 (+8.306µs): return nil
                                                                            Gather#157 ends at 46.858µs
                                                                        Gather#160 step 3/6 (+618ns): 52ns self time
                                                                        Gather#160 step 4/6 (+670ns): scatter:
                                                                          Task#155: pool=0
                                                                          Task#155 step 1/2 (+0s): 10.193µs self time
                                                                          Task#155 step 2/2 (+10.193µs): return nil
                                                                          Task#155 ends at 43.48µs
                                                                            Gather#155: index=0
                                                                            Gather#155 step 1/2 (+0s): 887ns self time
                                                                            Gather#155 step 2/2 (+887ns): return nil
                                                                            Gather#155 ends at 44.367µs
                                                                        Gather#160 step 5/6 (+670ns): 330ns self time
                                                                        Gather#160 step 6/6 (+1µs): return nil
                                                                        Gather#160 ends at 33.617µs
                                                                    Gather#162 step 3/4 (+6.266µs): 6.271µs self time
                                                                    Gather#162 step 4/4 (+12.537µs): return nil
                                                                    Gather#162 ends at 28.888µs
                                                                Gather#165 step 3/4 (+489ns): 498ns self time
                                                                Gather#165 step 4/4 (+987ns): return nil
                                                                Gather#165 ends at 13.612µs
                                                            Gather#167 step 3/12 (+173ns): 111ns self time
                                                            Gather#167 step 4/12 (+284ns): scatter:
                                                              Task#158: pool=0
                                                              Task#158 step 1/2 (+0s): 9.987µs self time
                                                              Task#158 step 2/2 (+9.987µs): return nil
                                                              Task#158 ends at 20.391µs
                                                                Gather#158: index=1
                                                                Gather#158 step 1/2 (+0s): 5.961µs self time
                                                                Gather#158 step 2/2 (+5.961µs): return nil
                                                                Gather#158 ends at 26.352µs
                                                            Gather#167 step 5/12 (+284ns): 186ns self time
                                                            Gather#167 step 6/12 (+470ns): scatter:
                                                              Task#152: pool=0
                                                              Task#152 step 1/2 (+0s): 11.324µs self time
                                                              Task#152 step 2/2 (+11.324µs): return nil
                                                              Task#152 ends at 21.914µs
                                                                Combine#152: index=0 flush=<nil>
                                                                Combine#152 step 1/2 (+0s): 1.001µs self time
                                                                Combine#152 step 2/2 (+1.001µs): return nil
                                                                Combine#152 ends at 22.915µs
                                                            Gather#167 step 7/12 (+470ns): 245ns self time
                                                            Gather#167 step 8/12 (+715ns): scatter:
                                                              Task#166: pool=0
                                                              Task#166 step 1/2 (+0s): 22.551µs self time
                                                              Task#166 step 2/2 (+22.551µs): return nil
                                                              Task#166 ends at 33.386µs
                                                                Gather#166: index=1
                                                                Gather#166 step 1/4 (+0s): 7ns self time
                                                                Gather#166 step 2/4 (+7ns): scatter:
                                                                  Task#161: pool=0
                                                                  Task#161 step 1/2 (+0s): 7.915µs self time
                                                                  Task#161 step 2/2 (+7.915µs): return nil
                                                                  Task#161 ends at 41.308µs
                                                                    Gather#161: index=4
                                                                    Gather#161 step 1/4 (+0s): 27.995µs self time
                                                                    Gather#161 step 2/4 (+27.995µs): scatter:
                                                                      Task#150: pool=0
                                                                      Task#150 step 1/2 (+0s): 9.999µs self time
                                                                      Task#150 step 2/2 (+9.999µs): return nil
                                                                      Task#150 ends at 79.302µs
                                                                        Combine#150: index=1 flush=Gather#150
                                                                        Combine#150 step 1/2 (+0s): 999ns self time
                                                                        Combine#150 step 2/2 (+999ns): return nil
                                                                        Combine#150 ends at 80.301µs
                                                                          Gather#150: index=1
                                                                          Gather#150 step 1/2 (+0s): 998ns self time
                                                                          Gather#150 step 2/2 (+998ns): return nil
                                                                          Gather#150 ends at 0s
                                                                    Gather#161 step 3/4 (+27.995µs): 17.628µs self time
                                                                    Gather#161 step 4/4 (+45.623µs): return nil
                                                                    Gather#161 ends at 86.931µs
                                                                Gather#166 step 3/4 (+7ns): 1.205µs self time
                                                                Gather#166 step 4/4 (+1.212µs): return nil
                                                                Gather#166 ends at 34.598µs
                                                            Gather#167 step 9/12 (+715ns): 114ns self time
                                                            Gather#167 step 10/12 (+829ns): scatter:
                                                              Task#164: pool=0
                                                              Task#164 step 1/2 (+0s): 9.999µs self time
                                                              Task#164 step 2/2 (+9.999µs): return nil
                                                              Task#164 ends at 20.948µs
                                                                Combine#164: index=11 flush=<nil>
                                                                Combine#164 step 1/10 (+0s): 175.963µs self time
                                                                Combine#164 step 2/10 (+175.963µs): scatter:
                                                                  Task#151: pool=0
                                                                  Task#151 step 1/2 (+0s): 6.793µs self time
                                                                  Task#151 step 2/2 (+6.793µs): return nil
                                                                  Task#151 ends at 203.704µs
                                                                    Gather#151: index=3
                                                                    Gather#151 step 1/2 (+0s): 488ns self time
                                                                    Gather#151 step 2/2 (+488ns): return nil
                                                                    Gather#151 ends at 204.192µs
                                                                Combine#164 step 3/10 (+175.963µs): 180.457µs self time
                                                                Combine#164 step 4/10 (+356.42µs): scatter:
                                                                  Task#153: pool=0
                                                                  Task#153 step 1/2 (+0s): 17.494µs self time
                                                                  Task#153 step 2/2 (+17.494µs): return nil
                                                                  Task#153 ends at 394.862µs
                                                                    Gather#153: index=1
                                                                    Gather#153 step 1/2 (+0s): 383ns self time
                                                                    Gather#153 step 2/2 (+383ns): return nil
                                                                    Gather#153 ends at 395.245µs
                                                                Combine#164 step 5/10 (+356.42µs): 118.77µs self time
                                                                Combine#164 step 6/10 (+475.19µs): scatter:
                                                                  Task#154: pool=0
                                                                  Task#154 step 1/2 (+0s): 10.007µs self time
                                                                  Task#154 step 2/2 (+10.007µs): return nil
                                                                  Task#154 ends at 506.145µs
                                                                    Gather#154: index=0
                                                                    Gather#154 step 1/2 (+0s): 1.044µs self time
                                                                    Gather#154 step 2/2 (+1.044µs): return nil
                                                                    Gather#154 ends at 507.189µs
                                                                Combine#164 step 7/10 (+475.19µs): 204.704µs self time
                                                                Combine#164 step 8/10 (+679.894µs): scatter:
                                                                  Task#159: pool=0
                                                                  Task#159 step 1/2 (+0s): 2.679412ms self time
                                                                  Task#159 step 2/2 (+2.679412ms): return nil
                                                                  Task#159 ends at 3.380254ms
                                                                    Gather#159: index=2
                                                                    Gather#159 step 1/2 (+0s): 7.186µs self time
                                                                    Gather#159 step 2/2 (+7.186µs): return nil
                                                                    Gather#159 ends at 3.38744ms
                                                                Combine#164 step 9/10 (+679.894µs): 204.709µs self time
                                                                Combine#164 step 10/10 (+884.603µs): return nil
                                                                Combine#164 ends at 905.551µs
                                                            Gather#167 step 11/12 (+829ns): 174ns self time
                                                            Gather#167 step 12/12 (+1.003µs): return nil
                                                            Gather#167 ends at 11.123µs
                                                        Plan#8 step 2/3 (+0s): scatter:
                                                          Task#168: pool=0
                                                          Task#168 step 1/2 (+0s): 3.140855ms self time
                                                          Task#168 step 2/2 (+3.140855ms): return nil
                                                          Task#168 ends at 3.140855ms
                                                            Combine#168: index=13 flush=<nil>
                                                            Combine#168 step 1/4 (+0s): 469ns self time
                                                            Combine#168 step 2/4 (+469ns): scatter:
                                                              Task#163: pool=0
                                                              Task#163 step 1/2 (+0s): 10µs self time
                                                              Task#163 step 2/2 (+10µs): return nil
                                                              Task#163 ends at 3.151324ms
                                                                Gather#163: index=0
                                                                Gather#163 step 1/4 (+0s): 854ns self time
                                                                Gather#163 step 2/4 (+854ns): scatter:
                                                                  Task#156: pool=0
                                                                  Task#156 step 1/2 (+0s): 52.14µs self time
                                                                  Task#156 step 2/2 (+52.14µs): return nil
                                                                  Task#156 ends at 3.204318ms
                                                                    Gather#156: index=1
                                                                    Gather#156 step 1/2 (+0s): 739ns self time
                                                                    Gather#156 step 2/2 (+739ns): return nil
                                                                    Gather#156 ends at 3.205057ms
                                                                Gather#163 step 3/4 (+854ns): 133ns self time
                                                                Gather#163 step 4/4 (+987ns): return nil
                                                                Gather#163 ends at 3.152311ms
                                                            Combine#168 step 3/4 (+469ns): 530ns self time
                                                            Combine#168 step 4/4 (+999ns): return nil
                                                            Combine#168 ends at 3.141854ms
                                                        Plan#8 step 3/3 (+0s): ends at 3.38744ms
                                                      Gather#149 step 3/4 (+3.388143ms): 132ns self time
                                                      Gather#149 step 4/4 (+3.388275ms): return error
                                                      Gather#149 ends at 9.549022ms
                                                  Combine#180 step 7/8 (+102ns): 593ns self time
                                                  Combine#180 step 8/8 (+695ns): return nil
                                                  Combine#180 ends at 470.938µs
                                              Combine#182 step 3/8 (+378.928µs): 20.391µs self time
                                              Combine#182 step 4/8 (+399.319µs): scatter:
                                                Task#173: pool=0
                                                Task#173 step 1/2 (+0s): 10.024µs self time
                                                Task#173 step 2/2 (+10.024µs): return nil
                                                Task#173 ends at 486.885µs
                                                  Gather#173: index=0
                                                  Gather#173 step 1/2 (+0s): 807ns self time
                                                  Gather#173 step 2/2 (+807ns): return nil
                                                  Gather#173 ends at 487.692µs
                                              Combine#182 step 5/8 (+399.319µs): 441.445µs self time
                                              Combine#182 step 6/8 (+840.764µs): scatter:
                                                Task#143: pool=1
                                                Task#143 step 1/2 (+0s): 10.005µs self time
                                                Task#143 step 2/2 (+10.005µs): return nil
                                                Task#143 ends at 928.311µs
                                                  Combine#143: index=0 flush=<nil>
                                                  Combine#143 step 1/2 (+0s): 1.49µs self time
                                                  Combine#143 step 2/2 (+1.49µs): return nil
                                                  Combine#143 ends at 929.801µs
                                              Combine#182 step 7/8 (+840.764µs): 30.264µs self time
                                              Combine#182 step 8/8 (+871.028µs): return nil
                                              Combine#182 ends at 948.57µs
                                          Combine#187 step 3/4 (+517ns): 507ns self time
                                          Combine#187 step 4/4 (+1.024µs): return nil
                                          Combine#187 ends at 67.846µs
                                      Combine#189 step 3/4 (+35.547µs): 35.722µs self time
                                      Combine#189 step 4/4 (+71.269µs): return nil
                                      Combine#189 ends at 92.515µs
                                  Combine#194 step 3/14 (+486ns): 22ns self time
                                  Combine#194 step 4/14 (+508ns): scatter:
                                    Task#190: pool=0
                                    Task#190 step 1/2 (+0s): 10.011µs self time
                                    Task#190 step 2/2 (+10.011µs): return nil
                                    Task#190 ends at 20.516µs
                                      Gather#190: index=0
                                      Gather#190 step 1/4 (+0s): 804ns self time
                                      Gather#190 step 2/4 (+804ns): scatter:
                                        Task#186: pool=4
                                        Task#186 step 1/2 (+0s): 9.999µs self time
                                        Task#186 step 2/2 (+9.999µs): return nil
                                        Task#186 ends at 31.319µs
                                          Gather#186: index=0
                                          Gather#186 step 1/10 (+0s): 883ns self time
                                          Gather#186 step 2/10 (+883ns): scatter:
                                            Task#184: pool=0
                                            Task#184 step 1/2 (+0s): 9.998µs self time
                                            Task#184 step 2/2 (+9.998µs): return nil
                                            Task#184 ends at 42.2µs
                                              Gather#184: index=0
                                              Gather#184 step 1/4 (+0s): 499ns self time
                                              Gather#184 step 2/4 (+499ns): scatter:
                                                Task#181: pool=1
                                                Task#181 step 1/2 (+0s): 8.274µs self time
                                                Task#181 step 2/2 (+8.274µs): return nil
                                                Task#181 ends at 50.973µs
                                                  Combine#181: index=8 flush=<nil>
                                                  Combine#181 step 1/8 (+0s): 336ns self time
                                                  Combine#181 step 2/8 (+336ns): scatter:
                                                    Task#136: pool=8
                                                    Task#136 step 1/2 (+0s): 23.904µs self time
                                                    Task#136 step 2/2 (+23.904µs): return nil
                                                    Task#136 ends at 75.213µs
                                                      Gather#136: index=0
                                                      Gather#136 step 1/2 (+0s): 1µs self time
                                                      Gather#136 step 2/2 (+1µs): return nil
                                                      Gather#136 ends at 76.213µs
                                                  Combine#181 step 3/8 (+336ns): 60ns self time
                                                  Combine#181 step 4/8 (+396ns): scatter:
                                                    Task#174: pool=9
                                                    Task#174 step 1/2 (+0s): 8.673321ms self time
                                                    Task#174 step 2/2 (+8.673321ms): return nil
                                                    Task#174 ends at 8.72469ms
                                                      Gather#174: index=0
                                                      Gather#174 step 1/2 (+0s): 995ns self time
                                                      Gather#174 step 2/2 (+995ns): return nil
                                                      Gather#174 ends at 8.725685ms
                                                  Combine#181 step 5/8 (+396ns): 787ns self time
                                                  Combine#181 step 6/8 (+1.183µs): scatter:
                                                    Task#137: pool=6
                                                    Task#137 step 1/2 (+0s): 9.998µs self time
                                                    Task#137 step 2/2 (+9.998µs): return nil
                                                    Task#137 ends at 62.154µs
                                                      Gather#137: index=0
                                                      Gather#137 step 1/2 (+0s): 999ns self time
                                                      Gather#137 step 2/2 (+999ns): return nil
                                                      Gather#137 ends at 63.153µs
                                                  Combine#181 step 7/8 (+1.183µs): 781ns self time
                                                  Combine#181 step 8/8 (+1.964µs): return nil
                                                  Combine#181 ends at 52.937µs
                                              Gather#184 step 3/4 (+499ns): 497ns self time
                                              Gather#184 step 4/4 (+996ns): return nil
                                              Gather#184 ends at 43.196µs
                                          Gather#186 step 3/10 (+883ns): 867ns self time
                                          Gather#186 step 4/10 (+1.75µs): scatter:
                                            Task#144: pool=7
                                            Task#144 step 1/2 (+0s): 10.154µs self time
                                            Task#144 step 2/2 (+10.154µs): return nil
                                            Task#144 ends at 43.223µs
                                              Gather#144: index=0
                                              Gather#144 step 1/2 (+0s): 970ns self time
                                              Gather#144 step 2/2 (+970ns): return nil
                                              Gather#144 ends at 44.193µs
                                          Gather#186 step 5/10 (+1.75µs): 345ns self time
                                          Gather#186 step 6/10 (+2.095µs): scatter:
                                            Task#183: pool=0
                                            Task#183 step 1/2 (+0s): 0s self time
                                            Task#183 step 2/2 (+0s): return nil
                                            Task#183 ends at 33.414µs
                                              Gather#183: index=0
                                              Gather#183 step 1/6 (+0s): 320ns self time
                                              Gather#183 step 2/6 (+320ns): scatter:
                                                Task#148: pool=8
                                                Task#148 step 1/2 (+0s): 10.037µs self time
                                                Task#148 step 2/2 (+10.037µs): return nil
                                                Task#148 ends at 43.771µs
                                                  Combine#148: index=2 flush=<nil>
                                                  Combine#148 step 1/2 (+0s): 755.321µs self time
                                                  Combine#148 step 2/2 (+755.321µs): return nil
                                                  Combine#148 ends at 799.092µs
                                              Gather#183 step 3/6 (+320ns): 352ns self time
                                              Gather#183 step 4/6 (+672ns): scatter:
                                                Task#139: pool=7
                                                Task#139 step 1/2 (+0s): 10µs self time
                                                Task#139 step 2/2 (+10µs): return nil
                                                Task#139 ends at 44.086µs
                                                  Combine#139: index=7 flush=<nil>
                                                  Combine#139 step 1/2 (+0s): 558.671µs self time
                                                  Combine#139 step 2/2 (+558.671µs): return nil
                                                  Combine#139 ends at 602.757µs
                                              Gather#183 step 5/6 (+672ns): 330ns self time
                                              Gather#183 step 6/6 (+1.002µs): return nil
                                              Gather#183 ends at 34.416µs
                                          Gather#186 step 7/10 (+2.095µs): 904ns self time
                                          Gather#186 step 8/10 (+2.999µs): scatter:
                                            Task#145: pool=6
                                            Task#145 step 1/2 (+0s): 10.007µs self time
                                            Task#145 step 2/2 (+10.007µs): return nil
                                            Task#145 ends at 44.325µs
                                              Combine#145: index=2 flush=<nil>
                                              Combine#145 step 1/2 (+0s): 995ns self time
                                              Combine#145 step 2/2 (+995ns): return nil
                                              Combine#145 ends at 45.32µs
                                          Gather#186 step 9/10 (+2.999µs): 357ns self time
                                          Gather#186 step 10/10 (+3.356µs): return nil
                                          Gather#186 ends at 34.675µs
                                      Gather#190 step 3/4 (+804ns): 807ns self time
                                      Gather#190 step 4/4 (+1.611µs): return nil
                                      Gather#190 ends at 22.127µs
                                  Combine#194 step 5/14 (+508ns): 81ns self time
                                  Combine#194 step 6/14 (+589ns): scatter:
                                    Task#175: pool=7
                                    Task#175 step 1/2 (+0s): 10.121µs self time
                                    Task#175 step 2/2 (+10.121µs): return nil
                                    Task#175 ends at 20.707µs
                                      Gather#175: index=0
                                      Gather#175 step 1/2 (+0s): 603ns self time
                                      Gather#175 step 2/2 (+603ns): return nil
                                      Gather#175 ends at 21.31µs
                                  Combine#194 step 7/14 (+589ns): 88ns self time
                                  Combine#194 step 8/14 (+677ns): scatter:
                                    Task#188: pool=5
                                    Task#188 step 1/2 (+0s): 8.236µs self time
                                    Task#188 step 2/2 (+8.236µs): return nil
                                    Task#188 ends at 18.91µs
                                      Combine#188: index=1 flush=<nil>
                                      Combine#188 step 1/8 (+0s): 225ns self time
                                      Combine#188 step 2/8 (+225ns): scatter:
                                        Task#185: pool=5
                                        Task#185 step 1/2 (+0s): 9.904µs self time
                                        Task#185 step 2/2 (+9.904µs): return nil
                                        Task#185 ends at 29.039µs
                                          Combine#185: index=6 flush=<nil>
                                          Combine#185 step 1/4 (+0s): 173ns self time
                                          Combine#185 step 2/4 (+173ns): scatter:
                                            Task#133: pool=2
                                            Task#133 step 1/2 (+0s): 2.563µs self time
                                            Task#133 step 2/2 (+2.563µs): return nil
                                            Task#133 ends at 31.775µs
                                              Gather#133: index=0
                                              Gather#133 step 1/2 (+0s): 988ns self time
                                              Gather#133 step 2/2 (+988ns): return nil
                                              Gather#133 ends at 32.763µs
                                          Combine#185 step 3/4 (+173ns): 828ns self time
                                          Combine#185 step 4/4 (+1.001µs): return nil
                                          Combine#185 ends at 30.04µs
                                      Combine#188 step 3/8 (+225ns): 469ns self time
                                      Combine#188 step 4/8 (+694ns): scatter:
                                        Task#140: pool=1
                                        Task#140 step 1/2 (+0s): 10µs self time
                                        Task#140 step 2/2 (+10µs): return nil
                                        Task#140 ends at 29.604µs
                                          Gather#140: index=0
                                          Gather#140 step 1/2 (+0s): 999ns self time
                                          Gather#140 step 2/2 (+999ns): return nil
                                          Gather#140 ends at 30.603µs
                                      Combine#188 step 5/8 (+694ns): 152ns self time
                                      Combine#188 step 6/8 (+846ns): scatter:
                                        Task#147: pool=2
                                        Task#147 step 1/2 (+0s): 15.346µs self time
                                        Task#147 step 2/2 (+15.346µs): return nil
                                        Task#147 ends at 35.102µs
                                          Gather#147: index=0
                                          Gather#147 step 1/2 (+0s): 874.864µs self time
                                          Gather#147 step 2/2 (+874.864µs): return nil
                                          Gather#147 ends at 909.966µs
                                      Combine#188 step 7/8 (+846ns): 156ns self time
                                      Combine#188 step 8/8 (+1.002µs): return nil
                                      Combine#188 ends at 19.912µs
                                  Combine#194 step 9/14 (+677ns): 81ns self time
                                  Combine#194 step 10/14 (+758ns): scatter:
                                    Task#138: pool=5
                                    Task#138 step 1/2 (+0s): 9.875µs self time
                                    Task#138 step 2/2 (+9.875µs): return nil
                                    Task#138 ends at 20.63µs
                                      Gather#138: index=0
                                      Gather#138 step 1/2 (+0s): 1.001µs self time
                                      Gather#138 step 2/2 (+1.001µs): return nil
                                      Gather#138 ends at 21.631µs
                                  Combine#194 step 11/14 (+758ns): 73ns self time
                                  Combine#194 step 12/14 (+831ns): scatter:
                                    Task#169: pool=7
                                    Task#169 step 1/2 (+0s): 10.014µs self time
                                    Task#169 step 2/2 (+10.014µs): return nil
                                    Task#169 ends at 20.842µs
                                      Gather#169: index=0
                                      Gather#169 step 1/2 (+0s): 1µs self time
                                      Gather#169 step 2/2 (+1µs): return nil
                                      Gather#169 ends at 21.842µs
                                  Combine#194 step 13/14 (+831ns): 79ns self time
                                  Combine#194 step 14/14 (+910ns): return nil
                                  Combine#194 ends at 10.907µs
                              Plan#7 step 2/6 (+0s): scatter:
                                Task#198: pool=6
                                Task#198 step 1/2 (+0s): 9.629µs self time
                                Task#198 step 2/2 (+9.629µs): return error
                                Task#198 ends at 9.629µs
                                  Gather#198: index=0
                                  Gather#198 step 1/6 (+0s): 245.836µs self time
                                  Gather#198 step 2/6 (+245.836µs): scatter:
                                    Task#134: pool=0
                                    Task#134 step 1/2 (+0s): 9.985µs self time
                                    Task#134 step 2/2 (+9.985µs): return nil
                                    Task#134 ends at 265.45µs
                                      Combine#134: index=7 flush=<nil>
                                      Combine#134 step 1/2 (+0s): 1µs self time
                                      Combine#134 step 2/2 (+1µs): return nil
                                      Combine#134 ends at 266.45µs
                                  Gather#198 step 3/6 (+245.836µs): 248.351µs self time
                                  Gather#198 step 4/6 (+494.187µs): scatter:
                                    Task#191: pool=1
                                    Task#191 step 1/2 (+0s): 10.114µs self time
                                    Task#191 step 2/2 (+10.114µs): return nil
                                    Task#191 ends at 513.93µs
                                      Gather#191: index=0
                                      Gather#191 step 1/8 (+0s): 456ns self time
                                      Gather#191 step 2/8 (+456ns): scatter:
                                        Task#141: pool=0
                                        Task#141 step 1/2 (+0s): 9.669µs self time
                                        Task#141 step 2/2 (+9.669µs): return nil
                                        Task#141 ends at 524.055µs
                                          Gather#141: index=0
                                          Gather#141 step 1/2 (+0s): 1.028µs self time
                                          Gather#141 step 2/2 (+1.028µs): return nil
                                          Gather#141 ends at 525.083µs
                                      Gather#191 step 3/8 (+456ns): 549ns self time
                                      Gather#191 step 4/8 (+1.005µs): scatter:
                                        Task#177: pool=9
                                        Task#177 step 1/2 (+0s): 20.772µs self time
                                        Task#177 step 2/2 (+20.772µs): return nil
                                        Task#177 ends at 535.707µs
                                          Combine#177: index=1 flush=<nil>
                                          Combine#177 step 1/2 (+0s): 18ns self time
                                          Combine#177 step 2/2 (+18ns): return nil
                                          Combine#177 ends at 535.725µs
                                      Gather#191 step 5/8 (+1.005µs): 175ns self time
                                      Gather#191 step 6/8 (+1.18µs): scatter:
                                        Task#135: pool=0
                                        Task#135 step 1/2 (+0s): 9.998µs self time
                                        Task#135 step 2/2 (+9.998µs): return nil
                                        Task#135 ends at 525.108µs
                                          Gather#135: index=0
                                          Gather#135 step 1/2 (+0s): 1µs self time
                                          Gather#135 step 2/2 (+1µs): return error
                                          Gather#135 ends at 526.108µs
                                      Gather#191 step 7/8 (+1.18µs): 628ns self time
                                      Gather#191 step 8/8 (+1.808µs): return nil
                                      Gather#191 ends at 515.738µs
                                  Gather#198 step 5/6 (+494.187µs): 248.353µs self time
                                  Gather#198 step 6/6 (+742.54µs): return nil
                                  Gather#198 ends at 752.169µs
                              Plan#7 step 3/6 (+0s): scatter:
                                Task#196: pool=1
                                Task#196 step 1/2 (+0s): 9.757µs self time
                                Task#196 step 2/2 (+9.757µs): return error
                                Task#196 ends at 9.757µs
                                  Combine#196: index=8 flush=<nil>
                                  Combine#196 step 1/10 (+0s): 428ns self time
                                  Combine#196 step 2/10 (+428ns): scatter:
                                    Task#193: pool=0
                                    Task#193 step 1/2 (+0s): 9.975µs self time
                                    Task#193 step 2/2 (+9.975µs): return nil
                                    Task#193 ends at 20.16µs
                                      Gather#193: index=0
                                      Gather#193 step 1/4 (+0s): 627ns self time
                                      Gather#193 step 2/4 (+627ns): scatter:
                                        Task#142: pool=2
                                        Task#142 step 1/2 (+0s): 5.708µs self time
                                        Task#142 step 2/2 (+5.708µs): return nil
                                        Task#142 ends at 26.495µs
                                          Combine#142: index=1 flush=<nil>
                                          Combine#142 step 1/2 (+0s): 1.001µs self time
                                          Combine#142 step 2/2 (+1.001µs): return nil
                                          Combine#142 ends at 27.496µs
                                      Gather#193 step 3/4 (+627ns): 629ns self time
                                      Gather#193 step 4/4 (+1.256µs): return nil
                                      Gather#193 ends at 21.416µs
                                  Combine#196 step 3/10 (+428ns): 143ns self time
                                  Combine#196 step 4/10 (+571ns): scatter:
                                    Task#146: pool=5
                                    Task#146 step 1/2 (+0s): 9.936µs self time
                                    Task#146 step 2/2 (+9.936µs): return nil
                                    Task#146 ends at 20.264µs
                                      Gather#146: index=0
                                      Gather#146 step 1/2 (+0s): 1.015µs self time
                                      Gather#146 step 2/2 (+1.015µs): return nil
                                      Gather#146 ends at 21.279µs
                                  Combine#196 step 5/10 (+571ns): 110ns self time
                                  Combine#196 step 6/10 (+681ns): scatter:
                                    Task#192: pool=1
                                    Task#192 step 1/2 (+0s): 10.002µs self time
                                    Task#192 step 2/2 (+10.002µs): return nil
                                    Task#192 ends at 20.44µs
                                      Gather#192: index=0
                                      Gather#192 step 1/4 (+0s): 294ns self time
                                      Gather#192 step 2/4 (+294ns): scatter:
                                        Task#172: pool=1
                                        Task#172 step 1/2 (+0s): 9.998µs self time
                                        Task#172 step 2/2 (+9.998µs): return nil
                                        Task#172 ends at 30.732µs
                                          Gather#172: index=0
                                          Gather#172 step 1/2 (+0s): 994ns self time
                                          Gather#172 step 2/2 (+994ns): return nil
                                          Gather#172 ends at 31.726µs
                                      Gather#192 step 3/4 (+294ns): 256ns self time
                                      Gather#192 step 4/4 (+550ns): return nil
                                      Gather#192 ends at 20.99µs
                                  Combine#196 step 7/10 (+681ns): 157ns self time
                                  Combine#196 step 8/10 (+838ns): scatter:
                                    Task#178: pool=4
                                    Task#178 step 1/2 (+0s): 146.857µs self time
                                    Task#178 step 2/2 (+146.857µs): return nil
                                    Task#178 ends at 157.452µs
                                      Combine#178: index=3 flush=<nil>
                                      Combine#178 step 1/2 (+0s): 160.609µs self time
                                      Combine#178 step 2/2 (+160.609µs): return nil
                                      Combine#178 ends at 318.061µs
                                  Combine#196 step 9/10 (+838ns): 153ns self time
                                  Combine#196 step 10/10 (+991ns): return nil
                                  Combine#196 ends at 10.748µs
                              Plan#7 step 4/6 (+0s): scatter:
                                Task#197: pool=3
                                Task#197 step 1/2 (+0s): 10.12µs self time
                                Task#197 step 2/2 (+10.12µs): return error
                                Task#197 ends at 10.12µs
                                  Gather#197: index=0
                                  Gather#197 step 1/4 (+0s): 166ns self time
                                  Gather#197 step 2/4 (+166ns): scatter:
                                    Task#170: pool=3
                                    Task#170 step 1/2 (+0s): 9.632µs self time
                                    Task#170 step 2/2 (+9.632µs): return nil
                                    Task#170 ends at 19.918µs
                                      Gather#170: index=0
                                      Gather#170 step 1/2 (+0s): 1.014µs self time
                                      Gather#170 step 2/2 (+1.014µs): return nil
                                      Gather#170 ends at 20.932µs
                                  Gather#197 step 3/4 (+166ns): 159ns self time
                                  Gather#197 step 4/4 (+325ns): return nil
                                  Gather#197 ends at 10.445µs
                              Plan#7 step 5/6 (+0s): scatter:
                                Task#195: pool=4
                                Task#195 step 1/2 (+0s): 7.412µs self time
                                Task#195 step 2/2 (+7.412µs): return nil
                                Task#195 ends at 7.412µs
                                  Combine#195: index=9 flush=<nil>
                                  Combine#195 step 1/4 (+0s): 0s self time
                                  Combine#195 step 2/4 (+0s): scatter:
                                    Task#176: pool=1
                                    Task#176 step 1/2 (+0s): 9.723µs self time
                                    Task#176 step 2/2 (+9.723µs): return nil
                                    Task#176 ends at 17.135µs
                                      Gather#176: index=0
                                      Gather#176 step 1/2 (+0s): 989ns self time
                                      Gather#176 step 2/2 (+989ns): return nil
                                      Gather#176 ends at 18.124µs
                                  Combine#195 step 3/4 (+0s): 0s self time
                                  Combine#195 step 4/4 (+0s): return nil
                                  Combine#195 ends at 7.412µs
                              Plan#7 step 6/6 (+0s): ends at 9.549022ms
                            Task#132 step 3/4 (+9.554083ms): 4.998µs self time
                            Task#132 step 4/4 (+9.559081ms): return nil
                            Task#132 ends at 19.845285ms
                              Combine#132: index=3 flush=<nil>
                              Combine#132 step 1/4 (+0s): 1.027µs self time
                              Combine#132 step 2/4 (+1.027µs): scatter:
                                Task#1: pool=9
                                Task#1 step 1/2 (+0s): 167.697µs self time
                                Task#1 step 2/2 (+167.697µs): return nil
                                Task#1 ends at 20.014009ms
                                  Gather#1: index=3
                                  Gather#1 step 1/2 (+0s): 809ns self time
                                  Gather#1 step 2/2 (+809ns): return nil
                                  Gather#1 ends at 20.014818ms
                              Combine#132 step 3/4 (+1.027µs): 0s self time
                              Combine#132 step 4/4 (+1.027µs): return nil
                              Combine#132 ends at 19.846312ms
                          Gather#269 step 3/6 (+249ns): 403ns self time
                          Gather#269 step 4/6 (+652ns): scatter:
                            Task#71: pool=0
                            Task#71 step 1/4 (+0s): 4.974µs self time
                            Task#71 step 2/4 (+4.974µs): subjob:
                              Plan#4: pathCount=4 taskCount=10 maxPathDuration=8.171756ms minGatherCount=9 maxGatherCount=15
                                 TaskPools[0]: TaskPool#21: limit=2
                                 CombinerPools[0]: CombinerPool#12: limit=4
                                 CombinerPools[1]: CombinerPool#13: limit=6
                                 CombinerPools[2]: CombinerPool#14: limit=3
                                 CombinerPools[3]: CombinerPool#15: limit=3
                                 CombinerPools[4]: CombinerPool#16: limit=2
                                 CombinerPools[5]: CombinerPool#17: limit=1
                                 Combiners[0]: pool=1
                              Plan#4 step 1/3 (+0s): scatter:
                                Task#124: pool=0
                                Task#124 step 1/2 (+0s): 13.665µs self time
                                Task#124 step 2/2 (+13.665µs): return nil
                                Task#124 ends at 13.665µs
                                  Gather#124: index=1
                                  Gather#124 step 1/4 (+0s): 139ns self time
                                  Gather#124 step 2/4 (+139ns): scatter:
                                    Task#123: pool=0
                                    Task#123 step 1/2 (+0s): 10.219µs self time
                                    Task#123 step 2/2 (+10.219µs): return error
                                    Task#123 ends at 24.023µs
                                      Gather#123: index=1
                                      Gather#123 step 1/4 (+0s): 322ns self time
                                      Gather#123 step 2/4 (+322ns): scatter:
                                        Task#122: pool=0
                                        Task#122 step 1/2 (+0s): 11.957µs self time
                                        Task#122 step 2/2 (+11.957µs): return nil
                                        Task#122 ends at 36.302µs
                                          Combine#122: index=0 flush=<nil>
                                          Combine#122 step 1/4 (+0s): 534ns self time
                                          Combine#122 step 2/4 (+534ns): scatter:
                                            Task#121: pool=0
                                            Task#121 step 1/2 (+0s): 9.095µs self time
                                            Task#121 step 2/2 (+9.095µs): return nil
                                            Task#121 ends at 45.931µs
                                              Gather#121: index=0
                                              Gather#121 step 1/8 (+0s): 17ns self time
                                              Gather#121 step 2/8 (+17ns): scatter:
                                                Task#120: pool=0
                                                Task#120 step 1/2 (+0s): 10.153µs self time
                                                Task#120 step 2/2 (+10.153µs): return error
                                                Task#120 ends at 56.101µs
                                                  Gather#120: index=1
                                                  Gather#120 step 1/4 (+0s): 0s self time
                                                  Gather#120 step 2/4 (+0s): scatter:
                                                    Task#102: pool=0
                                                    Task#102 step 1/2 (+0s): 120.818µs self time
                                                    Task#102 step 2/2 (+120.818µs): return nil
                                                    Task#102 ends at 176.919µs
                                                      Gather#102: index=0
                                                      Gather#102 step 1/2 (+0s): 1.169µs self time
                                                      Gather#102 step 2/2 (+1.169µs): return nil
                                                      Gather#102 ends at 178.088µs
                                                  Gather#120 step 3/4 (+0s): 0s self time
                                                  Gather#120 step 4/4 (+0s): return nil
                                                  Gather#120 ends at 56.101µs
                                              Gather#121 step 3/8 (+17ns): 5ns self time
                                              Gather#121 step 4/8 (+22ns): scatter:
                                                Task#72: pool=0
                                                Task#72 step 1/4 (+0s): 8.641µs self time
                                                Task#72 step 2/4 (+8.641µs): subjob:
                                                  Plan#5: pathCount=16 taskCount=28 maxPathDuration=8.114805ms minGatherCount=20 maxGatherCount=33
                                                     TaskPools[0]: TaskPool#22: limit=8
                                                     TaskPools[1]: TaskPool#23: limit=8
                                                     TaskPools[2]: TaskPool#24: limit=5
                                                     TaskPools[3]: TaskPool#25: limit=4
                                                     TaskPools[4]: TaskPool#26: limit=1
                                                     CombinerPools[0]: CombinerPool#18: limit=2
                                                     CombinerPools[1]: CombinerPool#19: limit=1
                                                     Combiners[0]: pool=1
                                                     Combiners[1]: pool=0
                                                     Combiners[2]: pool=0
                                                     Combiners[3]: pool=1
                                                     Combiners[4]: pool=0
                                                     Combiners[5]: pool=1
                                                     Combiners[6]: pool=0
                                                  Plan#5 step 1/4 (+0s): scatter:
                                                    Task#98: pool=3
                                                    Task#98 step 1/2 (+0s): 0s self time
                                                    Task#98 step 2/2 (+0s): return nil
                                                    Task#98 ends at 0s
                                                      Combine#98: index=4 flush=<nil>
                                                      Combine#98 step 1/4 (+0s): 1.222µs self time
                                                      Combine#98 step 2/4 (+1.222µs): scatter:
                                                        Task#95: pool=4
                                                        Task#95 step 1/2 (+0s): 0s self time
                                                        Task#95 step 2/2 (+0s): return nil
                                                        Task#95 ends at 1.222µs
                                                          Gather#95: index=4
                                                          Gather#95 step 1/8 (+0s): 162.526µs self time
                                                          Gather#95 step 2/8 (+162.526µs): scatter:
                                                            Task#94: pool=4
                                                            Task#94 step 1/2 (+0s): 9.99µs self time
                                                            Task#94 step 2/2 (+9.99µs): return nil
                                                            Task#94 ends at 173.738µs
                                                              Gather#94: index=5
                                                              Gather#94 step 1/8 (+0s): 5.695µs self time
                                                              Gather#94 step 2/8 (+5.695µs): scatter:
                                                                Task#90: pool=1
                                                                Task#90 step 1/2 (+0s): 9.885µs self time
                                                                Task#90 step 2/2 (+9.885µs): return nil
                                                                Task#90 ends at 189.318µs
                                                                  Combine#90: index=2 flush=<nil>
                                                                  Combine#90 step 1/10 (+0s): 36ns self time
                                                                  Combine#90 step 2/10 (+36ns): scatter:
                                                                    Task#76: pool=4
                                                                    Task#76 step 1/2 (+0s): 10.007µs self time
                                                                    Task#76 step 2/2 (+10.007µs): return nil
                                                                    Task#76 ends at 199.361µs
                                                                      Gather#76: index=6
                                                                      Gather#76 step 1/2 (+0s): 538.712µs self time
                                                                      Gather#76 step 2/2 (+538.712µs): return nil
                                                                      Gather#76 ends at 738.073µs
                                                                  Combine#90 step 3/10 (+36ns): 181ns self time
                                                                  Combine#90 step 4/10 (+217ns): scatter:
                                                                    Task#89: pool=1
                                                                    Task#89 step 1/2 (+0s): 8.283µs self time
                                                                    Task#89 step 2/2 (+8.283µs): return nil
                                                                    Task#89 ends at 197.818µs
                                                                      Gather#89: index=0
                                                                      Gather#89 step 1/4 (+0s): 471ns self time
                                                                      Gather#89 step 2/4 (+471ns): scatter:
                                                                        Task#80: pool=2
                                                                        Task#80 step 1/2 (+0s): 86.745µs self time
                                                                        Task#80 step 2/2 (+86.745µs): return nil
                                                                        Task#80 ends at 285.034µs
                                                                          Combine#80: index=3 flush=<nil>
                                                                          Combine#80 step 1/2 (+0s): 203ns self time
                                                                          Combine#80 step 2/2 (+203ns): return nil
                                                                          Combine#80 ends at 285.237µs
                                                                      Gather#89 step 3/4 (+471ns): 429ns self time
                                                                      Gather#89 step 4/4 (+900ns): return nil
                                                                      Gather#89 ends at 198.718µs
                                                                  Combine#90 step 5/10 (+217ns): 364ns self time
                                                                  Combine#90 step 6/10 (+581ns): scatter:
                                                                    Task#83: pool=4
                                                                    Task#83 step 1/2 (+0s): 9.997µs self time
                                                                    Task#83 step 2/2 (+9.997µs): return nil
                                                                    Task#83 ends at 199.896µs
                                                                      Gather#83: index=1
                                                                      Gather#83 step 1/2 (+0s): 1.353µs self time
                                                                      Gather#83 step 2/2 (+1.353µs): return nil
                                                                      Gather#83 ends at 201.249µs
                                                                  Combine#90 step 7/10 (+581ns): 102ns self time
                                                                  Combine#90 step 8/10 (+683ns): scatter:
                                                                    Task#86: pool=1
                                                                    Task#86 step 1/2 (+0s): 6.275µs self time
                                                                    Task#86 step 2/2 (+6.275µs): return nil
                                                                    Task#86 ends at 196.276µs
                                                                      Gather#86: index=1
                                                                      Gather#86 step 1/2 (+0s): 1.002µs self time
                                                                      Gather#86 step 2/2 (+1.002µs): return nil
                                                                      Gather#86 ends at 197.278µs
                                                                  Combine#90 step 9/10 (+683ns): 53ns self time
                                                                  Combine#90 step 10/10 (+736ns): return nil
                                                                  Combine#90 ends at 190.054µs
                                                              Gather#94 step 3/8 (+5.695µs): 6.163µs self time
                                                              Gather#94 step 4/8 (+11.858µs): scatter:
                                                                Task#87: pool=1
                                                                Task#87 step 1/2 (+0s): 10.028µs self time
                                                                Task#87 step 2/2 (+10.028µs): return nil
                                                                Task#87 ends at 195.624µs
                                                                  Gather#87: index=7
                                                                  Gather#87 step 1/2 (+0s): 0s self time
                                                                  Gather#87 step 2/2 (+0s): return nil
                                                                  Gather#87 ends at 195.624µs
                                                              Gather#94 step 5/8 (+11.858µs): 11.839µs self time
                                                              Gather#94 step 6/8 (+23.697µs): scatter:
                                                                Task#81: pool=3
                                                                Task#81 step 1/2 (+0s): 3.951324ms self time
                                                                Task#81 step 2/2 (+3.951324ms): return nil
                                                                Task#81 ends at 4.148759ms
                                                                  Gather#81: index=3
                                                                  Gather#81 step 1/2 (+0s): 1.069µs self time
                                                                  Gather#81 step 2/2 (+1.069µs): return nil
                                                                  Gather#81 ends at 4.149828ms
                                                              Gather#94 step 7/8 (+23.697µs): 492ns self time
                                                              Gather#94 step 8/8 (+24.189µs): return nil
                                                              Gather#94 ends at 197.927µs
                                                          Gather#95 step 3/8 (+162.526µs): 162.533µs self time
                                                          Gather#95 step 4/8 (+325.059µs): scatter:
                                                            Task#77: pool=1
                                                            Task#77 step 1/2 (+0s): 9.929µs self time
                                                            Task#77 step 2/2 (+9.929µs): return nil
                                                            Task#77 ends at 336.21µs
                                                              Combine#77: index=2 flush=<nil>
                                                              Combine#77 step 1/2 (+0s): 21.665µs self time
                                                              Combine#77 step 2/2 (+21.665µs): return nil
                                                              Combine#77 ends at 357.875µs
                                                          Gather#95 step 5/8 (+325.059µs): 162.532µs self time
                                                          Gather#95 step 6/8 (+487.591µs): scatter:
                                                            Task#91: pool=0
                                                            Task#91 step 1/2 (+0s): 11.014µs self time
                                                            Task#91 step 2/2 (+11.014µs): return nil
                                                            Task#91 ends at 499.827µs
                                                              Gather#91: index=7
                                                              Gather#91 step 1/4 (+0s): 495ns self time
                                                              Gather#91 step 2/4 (+495ns): scatter:
                                                                Task#85: pool=3
                                                                Task#85 step 1/2 (+0s): 2.885µs self time
                                                                Task#85 step 2/2 (+2.885µs): return nil
                                                                Task#85 ends at 503.207µs
                                                                  Gather#85: index=1
                                                                  Gather#85 step 1/2 (+0s): 959ns self time
                                                                  Gather#85 step 2/2 (+959ns): return nil
                                                                  Gather#85 ends at 504.166µs
                                                              Gather#91 step 3/4 (+495ns): 480ns self time
                                                              Gather#91 step 4/4 (+975ns): return nil
                                                              Gather#91 ends at 500.802µs
                                                          Gather#95 step 7/8 (+487.591µs): 162.534µs self time
                                                          Gather#95 step 8/8 (+650.125µs): return nil
                                                          Gather#95 ends at 651.347µs
                                                      Combine#98 step 3/4 (+1.222µs): 1.233µs self time
                                                      Combine#98 step 4/4 (+2.455µs): return nil
                                                      Combine#98 ends at 2.455µs
                                                  Plan#5 step 2/4 (+0s): scatter:
                                                    Task#100: pool=2
                                                    Task#100 step 1/2 (+0s): 10µs self time
                                                    Task#100 step 2/2 (+10µs): return nil
                                                    Task#100 ends at 10µs
                                                      Gather#100: index=6
                                                      Gather#100 step 1/10 (+0s): 231ns self time
                                                      Gather#100 step 2/10 (+231ns): scatter:
                                                        Task#97: pool=3
                                                        Task#97 step 1/2 (+0s): 8.966µs self time
                                                        Task#97 step 2/2 (+8.966µs): return nil
                                                        Task#97 ends at 19.197µs
                                                          Gather#97: index=0
                                                          Gather#97 step 1/4 (+0s): 609ns self time
                                                          Gather#97 step 2/4 (+609ns): scatter:
                                                            Task#92: pool=4
                                                            Task#92 step 1/2 (+0s): 622.677µs self time
                                                            Task#92 step 2/2 (+622.677µs): return nil
                                                            Task#92 ends at 642.483µs
                                                              Gather#92: index=4
                                                              Gather#92 step 1/4 (+0s): 37ns self time
                                                              Gather#92 step 2/4 (+37ns): scatter:
                                                                Task#88: pool=4
                                                                Task#88 step 1/2 (+0s): 9.902µs self time
                                                                Task#88 step 2/2 (+9.902µs): return nil
                                                                Task#88 ends at 652.422µs
                                                                  Combine#88: index=1 flush=<nil>
                                                                  Combine#88 step 1/2 (+0s): 782ns self time
                                                                  Combine#88 step 2/2 (+782ns): return nil
                                                                  Combine#88 ends at 653.204µs
                                                              Gather#92 step 3/4 (+37ns): 177ns self time
                                                              Gather#92 step 4/4 (+214ns): return nil
                                                              Gather#92 ends at 642.697µs
                                                          Gather#97 step 3/4 (+609ns): 390ns self time
                                                          Gather#97 step 4/4 (+999ns): return nil
                                                          Gather#97 ends at 20.196µs
                                                      Gather#100 step 3/10 (+231ns): 507ns self time
                                                      Gather#100 step 4/10 (+738ns): scatter:
                                                        Task#74: pool=1
                                                        Task#74 step 1/2 (+0s): 10.003µs self time
                                                        Task#74 step 2/2 (+10.003µs): return nil
                                                        Task#74 ends at 20.741µs
                                                          Gather#74: index=2
                                                          Gather#74 step 1/2 (+0s): 473.961µs self time
                                                          Gather#74 step 2/2 (+473.961µs): return nil
                                                          Gather#74 ends at 494.702µs
                                                      Gather#100 step 5/10 (+738ns): 68ns self time
                                                      Gather#100 step 6/10 (+806ns): scatter:
                                                        Task#96: pool=4
                                                        Task#96 step 1/2 (+0s): 10.01µs self time
                                                        Task#96 step 2/2 (+10.01µs): return nil
                                                        Task#96 ends at 20.816µs
                                                          Combine#96: index=5 flush=<nil>
                                                          Combine#96 step 1/10 (+0s): 311ns self time
                                                          Combine#96 step 2/10 (+311ns): scatter:
                                                            Task#73: pool=3
                                                            Task#73 step 1/2 (+0s): 12.79µs self time
                                                            Task#73 step 2/2 (+12.79µs): return nil
                                                            Task#73 ends at 33.917µs
                                                              Gather#73: index=1
                                                              Gather#73 step 1/2 (+0s): 743ns self time
                                                              Gather#73 step 2/2 (+743ns): return nil
                                                              Gather#73 ends at 34.66µs
                                                          Combine#96 step 3/10 (+311ns): 107ns self time
                                                          Combine#96 step 4/10 (+418ns): scatter:
                                                            Task#78: pool=0
                                                            Task#78 step 1/2 (+0s): 10.001µs self time
                                                            Task#78 step 2/2 (+10.001µs): return nil
                                                            Task#78 ends at 31.235µs
                                                              Combine#78: index=3 flush=<nil>
                                                              Combine#78 step 1/2 (+0s): 987ns self time
                                                              Combine#78 step 2/2 (+987ns): return nil
                                                              Combine#78 ends at 32.222µs
                                                          Combine#96 step 5/10 (+418ns): 176ns self time
                                                          Combine#96 step 6/10 (+594ns): scatter:
                                                            Task#82: pool=1
                                                            Task#82 step 1/2 (+0s): 132.181µs self time
                                                            Task#82 step 2/2 (+132.181µs): return nil
                                                            Task#82 ends at 153.591µs
                                                              Combine#82: index=2 flush=<nil>
                                                              Combine#82 step 1/2 (+0s): 1.026µs self time
                                                              Combine#82 step 2/2 (+1.026µs): return nil
                                                              Combine#82 ends at 154.617µs
                                                          Combine#96 step 7/10 (+594ns): 186ns self time
                                                          Combine#96 step 8/10 (+780ns): scatter:
                                                            Task#93: pool=2
                                                            Task#93 step 1/2 (+0s): 9.99µs self time
                                                            Task#93 step 2/2 (+9.99µs): return nil
                                                            Task#93 ends at 31.586µs
                                                              Gather#93: index=1
                                                              Gather#93 step 1/4 (+0s): 499ns self time
                                                              Gather#93 step 2/4 (+499ns): scatter:
                                                                Task#84: pool=2
                                                                Task#84 step 1/2 (+0s): 10.372µs self time
                                                                Task#84 step 2/2 (+10.372µs): return nil
                                                                Task#84 ends at 42.457µs
                                                                  Gather#84: index=1
                                                                  Gather#84 step 1/2 (+0s): 980ns self time
                                                                  Gather#84 step 2/2 (+980ns): return nil
                                                                  Gather#84 ends at 43.437µs
                                                              Gather#93 step 3/4 (+499ns): 503ns self time
                                                              Gather#93 step 4/4 (+1.002µs): return nil
                                                              Gather#93 ends at 32.588µs
                                                          Combine#96 step 9/10 (+780ns): 218ns self time
                                                          Combine#96 step 10/10 (+998ns): return nil
                                                          Combine#96 ends at 21.814µs
                                                      Gather#100 step 7/10 (+806ns): 68ns self time
                                                      Gather#100 step 8/10 (+874ns): scatter:
                                                        Task#79: pool=0
                                                        Task#79 step 1/2 (+0s): 9.998µs self time
                                                        Task#79 step 2/2 (+9.998µs): return nil
                                                        Task#79 ends at 20.872µs
                                                          Gather#79: index=7
                                                          Gather#79 step 1/2 (+0s): 280ns self time
                                                          Gather#79 step 2/2 (+280ns): return nil
                                                          Gather#79 ends at 21.152µs
                                                      Gather#100 step 9/10 (+874ns): 124ns self time
                                                      Gather#100 step 10/10 (+998ns): return nil
                                                      Gather#100 ends at 10.998µs
                                                  Plan#5 step 3/4 (+0s): scatter:
                                                    Task#99: pool=0
                                                    Task#99 step 1/2 (+0s): 8.10302ms self time
                                                    Task#99 step 2/2 (+8.10302ms): return nil
                                                    Task#99 ends at 8.10302ms
                                                      Gather#99: index=3
                                                      Gather#99 step 1/4 (+0s): 551ns self time
                                                      Gather#99 step 2/4 (+551ns): scatter:
                                                        Task#75: pool=4
                                                        Task#75 step 1/2 (+0s): 10.231µs self time
                                                        Task#75 step 2/2 (+10.231µs): return nil
                                                        Task#75 ends at 8.113802ms
                                                          Gather#75: index=1
                                                          Gather#75 step 1/2 (+0s): 1.003µs self time
                                                          Gather#75 step 2/2 (+1.003µs): return nil
                                                          Gather#75 ends at 8.114805ms
                                                      Gather#99 step 3/4 (+551ns): 447ns self time
                                                      Gather#99 step 4/4 (+998ns): return nil
                                                      Gather#99 ends at 8.104018ms
                                                  Plan#5 step 4/4 (+0s): ends at 8.114805ms
                                                Task#72 step 3/4 (+8.123446ms): 1.357µs self time
                                                Task#72 step 4/4 (+8.124803ms): return nil
                                                Task#72 ends at 8.170756ms
                                                  Gather#72: index=0
                                                  Gather#72 step 1/2 (+0s): 1µs self time
                                                  Gather#72 step 2/2 (+1µs): return nil
                                                  Gather#72 ends at 8.171756ms
                                              Gather#121 step 5/8 (+22ns): 31ns self time
                                              Gather#121 step 6/8 (+53ns): scatter:
                                                Task#101: pool=0
                                                Task#101 step 1/2 (+0s): 13.721µs self time
                                                Task#101 step 2/2 (+13.721µs): return nil
                                                Task#101 ends at 59.705µs
                                                  Gather#101: index=1
                                                  Gather#101 step 1/2 (+0s): 1.075µs self time
                                                  Gather#101 step 2/2 (+1.075µs): return nil
                                                  Gather#101 ends at 60.78µs
                                              Gather#121 step 7/8 (+53ns): 25ns self time
                                              Gather#121 step 8/8 (+78ns): return nil
                                              Gather#121 ends at 46.009µs
                                          Combine#122 step 3/4 (+534ns): 530ns self time
                                          Combine#122 step 4/4 (+1.064µs): return nil
                                          Combine#122 ends at 37.366µs
                                      Gather#123 step 3/4 (+322ns): 61ns self time
                                      Gather#123 step 4/4 (+383ns): return nil
                                      Gather#123 ends at 24.406µs
                                  Gather#124 step 3/4 (+139ns): 151ns self time
                                  Gather#124 step 4/4 (+290ns): return nil
                                  Gather#124 ends at 13.955µs
                              Plan#4 step 2/3 (+0s): scatter:
                                Task#125: pool=0
                                Task#125 step 1/2 (+0s): 7.234µs self time
                                Task#125 step 2/2 (+7.234µs): return error
                                Task#125 ends at 7.234µs
                                  Gather#125: index=1
                                  Gather#125 step 1/4 (+0s): 189ns self time
                                  Gather#125 step 2/4 (+189ns): scatter:
                                    Task#103: pool=0
                                    Task#103 step 1/4 (+0s): 5µs self time
                                    Task#103 step 2/4 (+5µs): subjob:
                                      Plan#6: pathCount=7 taskCount=16 maxPathDuration=990.546µs minGatherCount=12 maxGatherCount=35
                                         TaskPools[0]: TaskPool#27: limit=1
                                         TaskPools[1]: TaskPool#28: limit=1
                                         TaskPools[2]: TaskPool#29: limit=8
                                         TaskPools[3]: TaskPool#30: limit=1
                                         TaskPools[4]: TaskPool#31: limit=4
                                         TaskPools[5]: TaskPool#32: limit=7
                                         TaskPools[6]: TaskPool#33: limit=2
                                         TaskPools[7]: TaskPool#34: limit=2
                                         TaskPools[8]: TaskPool#35: limit=4
                                         CombinerPools[0]: CombinerPool#20: limit=6
                                         CombinerPools[1]: CombinerPool#21: limit=9
                                         CombinerPools[2]: CombinerPool#22: limit=8
                                         CombinerPools[3]: CombinerPool#23: limit=1
                                         CombinerPools[4]: CombinerPool#24: limit=8
                                         CombinerPools[5]: CombinerPool#25: limit=4
                                         CombinerPools[6]: CombinerPool#26: limit=3
                                         CombinerPools[7]: CombinerPool#27: limit=1
                                         Combiners[0]: pool=5
                                         Combiners[1]: pool=1
                                         Combiners[2]: pool=0
                                         Combiners[3]: pool=7
                                         Combiners[4]: pool=0
                                      Plan#6 step 1/2 (+0s): scatter:
                                        Task#119: pool=3
                                        Task#119 step 1/2 (+0s): 1.344µs self time
                                        Task#119 step 2/2 (+1.344µs): return nil
                                        Task#119 ends at 1.344µs
                                          Gather#119: index=2
                                          Gather#119 step 1/8 (+0s): 147ns self time
                                          Gather#119 step 2/8 (+147ns): scatter:
                                            Task#116: pool=7
                                            Task#116 step 1/2 (+0s): 1.295µs self time
                                            Task#116 step 2/2 (+1.295µs): return nil
                                            Task#116 ends at 2.786µs
                                              Gather#116: index=0
                                              Gather#116 step 1/4 (+0s): 510ns self time
                                              Gather#116 step 2/4 (+510ns): scatter:
                                                Task#115: pool=2
                                                Task#115 step 1/2 (+0s): 6.166µs self time
                                                Task#115 step 2/2 (+6.166µs): return nil
                                                Task#115 ends at 9.462µs
                                                  Gather#115: index=3
                                                  Gather#115 step 1/4 (+0s): 11ns self time
                                                  Gather#115 step 2/4 (+11ns): scatter:
                                                    Task#113: pool=1
                                                    Task#113 step 1/2 (+0s): 10.323µs self time
                                                    Task#113 step 2/2 (+10.323µs): return nil
                                                    Task#113 ends at 19.796µs
                                                      Gather#113: index=1
                                                      Gather#113 step 1/4 (+0s): 945.682µs self time
                                                      Gather#113 step 2/4 (+945.682µs): scatter:
                                                        Task#104: pool=2
                                                        Task#104 step 1/2 (+0s): 10.486µs self time
                                                        Task#104 step 2/2 (+10.486µs): return nil
                                                        Task#104 ends at 975.964µs
                                                          Combine#104: index=3 flush=<nil>
                                                          Combine#104 step 1/2 (+0s): 556ns self time
                                                          Combine#104 step 2/2 (+556ns): return nil
                                                          Combine#104 ends at 976.52µs
                                                      Gather#113 step 3/4 (+945.682µs): 25.068µs self time
                                                      Gather#113 step 4/4 (+970.75µs): return nil
                                                      Gather#113 ends at 990.546µs
                                                  Gather#115 step 3/4 (+11ns): 13ns self time
                                                  Gather#115 step 4/4 (+24ns): return nil
                                                  Gather#115 ends at 9.486µs
                                              Gather#116 step 3/4 (+510ns): 453ns self time
                                              Gather#116 step 4/4 (+963ns): return nil
                                              Gather#116 ends at 3.749µs
                                          Gather#119 step 3/8 (+147ns): 14ns self time
                                          Gather#119 step 4/8 (+161ns): scatter:
                                            Task#117: pool=1
                                            Task#117 step 1/2 (+0s): 12.283µs self time
                                            Task#117 step 2/2 (+12.283µs): return nil
                                            Task#117 ends at 13.788µs
                                              Gather#117: index=0
                                              Gather#117 step 1/4 (+0s): 58.903µs self time
                                              Gather#117 step 2/4 (+58.903µs): scatter:
                                                Task#114: pool=6
                                                Task#114 step 1/2 (+0s): 9.945µs self time
                                                Task#114 step 2/2 (+9.945µs): return nil
                                                Task#114 ends at 82.636µs
                                                  Combine#114: index=0 flush=<nil>
                                                  Combine#114 step 1/6 (+0s): 481ns self time
                                                  Combine#114 step 2/6 (+481ns): scatter:
                                                    Task#109: pool=1
                                                    Task#109 step 1/2 (+0s): 9.998µs self time
                                                    Task#109 step 2/2 (+9.998µs): return nil
                                                    Task#109 ends at 93.115µs
                                                      Combine#109: index=1 flush=<nil>
                                                      Combine#109 step 1/2 (+0s): 967ns self time
                                                      Combine#109 step 2/2 (+967ns): return nil
                                                      Combine#109 ends at 94.082µs
                                                  Combine#114 step 3/6 (+481ns): 7ns self time
                                                  Combine#114 step 4/6 (+488ns): scatter:
                                                    Task#112: pool=7
                                                    Task#112 step 1/2 (+0s): 9.999µs self time
                                                    Task#112 step 2/2 (+9.999µs): return nil
                                                    Task#112 ends at 93.123µs
                                                      Gather#112: index=4
                                                      Gather#112 step 1/4 (+0s): 517ns self time
                                                      Gather#112 step 2/4 (+517ns): scatter:
                                                        Task#111: pool=6
                                                        Task#111 step 1/2 (+0s): 74.955µs self time
                                                        Task#111 step 2/2 (+74.955µs): return nil
                                                        Task#111 ends at 168.595µs
                                                          Gather#111: index=0
                                                          Gather#111 step 1/10 (+0s): 125ns self time
                                                          Gather#111 step 2/10 (+125ns): scatter:
                                                            Task#107: pool=1
                                                            Task#107 step 1/2 (+0s): 10µs self time
                                                            Task#107 step 2/2 (+10µs): return nil
                                                            Task#107 ends at 178.72µs
                                                              Gather#107: index=0
                                                              Gather#107 step 1/2 (+0s): 998ns self time
                                                              Gather#107 step 2/2 (+998ns): return nil
                                                              Gather#107 ends at 179.718µs
                                                          Gather#111 step 3/10 (+125ns): 139ns self time
                                                          Gather#111 step 4/10 (+264ns): scatter:
                                                            Task#105: pool=0
                                                            Task#105 step 1/2 (+0s): 47.952µs self time
                                                            Task#105 step 2/2 (+47.952µs): return nil
                                                            Task#105 ends at 216.811µs
                                                              Gather#105: index=2
                                                              Gather#105 step 1/2 (+0s): 997ns self time
                                                              Gather#105 step 2/2 (+997ns): return nil
                                                              Gather#105 ends at 217.808µs
                                                          Gather#111 step 5/10 (+264ns): 182ns self time
                                                          Gather#111 step 6/10 (+446ns): scatter:
                                                            Task#108: pool=6
                                                            Task#108 step 1/2 (+0s): 10µs self time
                                                            Task#108 step 2/2 (+10µs): return nil
                                                            Task#108 ends at 179.041µs
                                                              Gather#108: index=4
                                                              Gather#108 step 1/2 (+0s): 1.195µs self time
                                                              Gather#108 step 2/2 (+1.195µs): return nil
                                                              Gather#108 ends at 180.236µs
                                                          Gather#111 step 7/10 (+446ns): 112ns self time
                                                          Gather#111 step 8/10 (+558ns): scatter:
                                                            Task#110: pool=8
                                                            Task#110 step 1/2 (+0s): 10.375µs self time
                                                            Task#110 step 2/2 (+10.375µs): return error
                                                            Task#110 ends at 179.528µs
                                                              Gather#110: index=4
                                                              Gather#110 step 1/2 (+0s): 998ns self time
                                                              Gather#110 step 2/2 (+998ns): return nil
                                                              Gather#110 ends at 180.526µs
                                                          Gather#111 step 9/10 (+558ns): 39ns self time
                                                          Gather#111 step 10/10 (+597ns): return nil
                                                          Gather#111 ends at 169.192µs
                                                      Gather#112 step 3/4 (+517ns): 480ns self time
                                                      Gather#112 step 4/4 (+997ns): return nil
                                                      Gather#112 ends at 94.12µs
                                                  Combine#114 step 5/6 (+488ns): 957ns self time
                                                  Combine#114 step 6/6 (+1.445µs): return nil
                                                  Combine#114 ends at 84.081µs
                                              Gather#117 step 3/4 (+58.903µs): 58.934µs self time
                                              Gather#117 step 4/4 (+117.837µs): return nil
                                              Gather#117 ends at 131.625µs
                                          Gather#119 step 5/8 (+161ns): 199ns self time
                                          Gather#119 step 6/8 (+360ns): scatter:
                                            Task#118: pool=1
                                            Task#118 step 1/2 (+0s): 10.001µs self time
                                            Task#118 step 2/2 (+10.001µs): return nil
                                            Task#118 ends at 11.705µs
                                              Combine#118: index=1 flush=<nil>
                                              Combine#118 step 1/4 (+0s): 0s self time
                                              Combine#118 step 2/4 (+0s): scatter:
                                                Task#106: pool=2
                                                Task#106 step 1/2 (+0s): 10.001µs self time
                                                Task#106 step 2/2 (+10.001µs): return nil
                                                Task#106 ends at 21.706µs
                                                  Gather#106: index=0
                                                  Gather#106 step 1/2 (+0s): 999ns self time
                                                  Gather#106 step 2/2 (+999ns): return nil
                                                  Gather#106 ends at 22.705µs
                                              Combine#118 step 3/4 (+0s): 0s self time
                                              Combine#118 step 4/4 (+0s): return nil
                                              Combine#118 ends at 11.705µs
                                          Gather#119 step 7/8 (+360ns): 219ns self time
                                          Gather#119 step 8/8 (+579ns): return error
                                          Gather#119 ends at 1.923µs
                                      Plan#6 step 2/2 (+0s): ends at 990.546µs
                                    Task#103 step 3/4 (+995.546µs): 4.998µs self time
                                    Task#103 step 4/4 (+1.000544ms): return nil
                                    Task#103 ends at 1.007967ms
                                      Gather#103: index=1
                                      Gather#103 step 1/2 (+0s): 1.009µs self time
                                      Gather#103 step 2/2 (+1.009µs): return nil
                                      Gather#103 ends at 1.008976ms
                                  Gather#125 step 3/4 (+189ns): 784ns self time
                                  Gather#125 step 4/4 (+973ns): return error
                                  Gather#125 ends at 8.207µs
                              Plan#4 step 3/3 (+0s): ends at 8.171756ms
                            Task#71 step 3/4 (+8.17673ms): 4.977µs self time
                            Task#71 step 4/4 (+8.181707ms): return nil
                            Task#71 ends at 18.468314ms
                              Gather#71: index=3
                              Gather#71 step 1/2 (+0s): 30ns self time
                              Gather#71 step 2/2 (+30ns): return nil
                              Gather#71 ends at 18.468344ms
                          Gather#269 step 5/6 (+652ns): 403ns self time
                          Gather#269 step 6/6 (+1.055µs): return nil
                          Gather#269 ends at 10.28701ms
                      Gather#272 step 3/6 (+300ns): 306ns self time
                      Gather#272 step 4/6 (+606ns): scatter:
                        Task#202: pool=2
                        Task#202 step 1/4 (+0s): 9.984µs self time
                        Task#202 step 2/4 (+9.984µs): subjob:
                          Plan#9: pathCount=14 taskCount=26 maxPathDuration=14.855613ms minGatherCount=20 maxGatherCount=32
                             TaskPools[0]: TaskPool#47: limit=1
                             TaskPools[1]: TaskPool#48: limit=7
                             CombinerPools[0]: CombinerPool#41: limit=2
                             CombinerPools[1]: CombinerPool#42: limit=2
                             Combiners[0]: pool=0
                             Combiners[1]: pool=0
                             Combiners[2]: pool=1
                          Plan#9 step 1/5 (+0s): scatter:
                            Task#268: pool=1
                            Task#268 step 1/2 (+0s): 10.003µs self time
                            Task#268 step 2/2 (+10.003µs): return nil
                            Task#268 ends at 10.003µs
                              Gather#268: index=8
                              Gather#268 step 1/8 (+0s): 0s self time
                              Gather#268 step 2/8 (+0s): scatter:
                                Task#204: pool=0
                                Task#204 step 1/2 (+0s): 9.994µs self time
                                Task#204 step 2/2 (+9.994µs): return nil
                                Task#204 ends at 19.997µs
                                  Gather#204: index=7
                                  Gather#204 step 1/2 (+0s): 1.096µs self time
                                  Gather#204 step 2/2 (+1.096µs): return nil
                                  Gather#204 ends at 21.093µs
                              Gather#268 step 3/8 (+0s): 0s self time
                              Gather#268 step 4/8 (+0s): scatter:
                                Task#264: pool=0
                                Task#264 step 1/2 (+0s): 9.997µs self time
                                Task#264 step 2/2 (+9.997µs): return nil
                                Task#264 ends at 20µs
                                  Combine#264: index=2 flush=<nil>
                                  Combine#264 step 1/6 (+0s): 303ns self time
                                  Combine#264 step 2/6 (+303ns): scatter:
                                    Task#211: pool=0
                                    Task#211 step 1/2 (+0s): 16.059µs self time
                                    Task#211 step 2/2 (+16.059µs): return nil
                                    Task#211 ends at 36.362µs
                                      Gather#211: index=11
                                      Gather#211 step 1/4 (+0s): 496ns self time
                                      Gather#211 step 2/4 (+496ns): subjob:
                                        Plan#10: pathCount=5 taskCount=12 maxPathDuration=869.582µs minGatherCount=9 maxGatherCount=12
                                           TaskPools[0]: TaskPool#49: limit=2
                                           TaskPools[1]: TaskPool#50: limit=1
                                           TaskPools[2]: TaskPool#51: limit=10
                                           TaskPools[3]: TaskPool#52: limit=6
                                           TaskPools[4]: TaskPool#53: limit=9
                                           TaskPools[5]: TaskPool#54: limit=1
                                           TaskPools[6]: TaskPool#55: limit=5
                                           TaskPools[7]: TaskPool#56: limit=2
                                           TaskPools[8]: TaskPool#57: limit=5
                                           TaskPools[9]: TaskPool#58: limit=5
                                           CombinerPools[0]: CombinerPool#43: limit=1
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
                                        Plan#10 step 1/2 (+0s): scatter:
                                          Task#223: pool=4
                                          Task#223 step 1/2 (+0s): 9.996µs self time
                                          Task#223 step 2/2 (+9.996µs): return nil
                                          Task#223 ends at 9.996µs
                                            Gather#223: index=6
                                            Gather#223 step 1/6 (+0s): 286.526µs self time
                                            Gather#223 step 2/6 (+286.526µs): scatter:
                                              Task#222: pool=6
                                              Task#222 step 1/2 (+0s): 1.415µs self time
                                              Task#222 step 2/2 (+1.415µs): return error
                                              Task#222 ends at 297.937µs
                                                Combine#222: index=5 flush=<nil>
                                                Combine#222 step 1/8 (+0s): 963ns self time
                                                Combine#222 step 2/8 (+963ns): scatter:
                                                  Task#213: pool=6
                                                  Task#213 step 1/2 (+0s): 10.018µs self time
                                                  Task#213 step 2/2 (+10.018µs): return nil
                                                  Task#213 ends at 308.918µs
                                                    Gather#213: index=5
                                                    Gather#213 step 1/2 (+0s): 993ns self time
                                                    Gather#213 step 2/2 (+993ns): return nil
                                                    Gather#213 ends at 309.911µs
                                                Combine#222 step 3/8 (+963ns): 991ns self time
                                                Combine#222 step 4/8 (+1.954µs): scatter:
                                                  Task#220: pool=1
                                                  Task#220 step 1/2 (+0s): 9.999µs self time
                                                  Task#220 step 2/2 (+9.999µs): return nil
                                                  Task#220 ends at 309.89µs
                                                    Gather#220: index=6
                                                    Gather#220 step 1/4 (+0s): 498ns self time
                                                    Gather#220 step 2/4 (+498ns): scatter:
                                                      Task#218: pool=4
                                                      Task#218 step 1/2 (+0s): 9.997µs self time
                                                      Task#218 step 2/2 (+9.997µs): return nil
                                                      Task#218 ends at 320.385µs
                                                        Gather#218: index=1
                                                        Gather#218 step 1/4 (+0s): 399ns self time
                                                        Gather#218 step 2/4 (+399ns): scatter:
                                                          Task#217: pool=1
                                                          Task#217 step 1/2 (+0s): 9.997µs self time
                                                          Task#217 step 2/2 (+9.997µs): return nil
                                                          Task#217 ends at 330.781µs
                                                            Gather#217: index=0
                                                            Gather#217 step 1/4 (+0s): 495ns self time
                                                            Gather#217 step 2/4 (+495ns): scatter:
                                                              Task#212: pool=2
                                                              Task#212 step 1/2 (+0s): 9.942µs self time
                                                              Task#212 step 2/2 (+9.942µs): return nil
                                                              Task#212 ends at 341.218µs
                                                                Gather#212: index=4
                                                                Gather#212 step 1/2 (+0s): 996ns self time
                                                                Gather#212 step 2/2 (+996ns): return nil
                                                                Gather#212 ends at 342.214µs
                                                            Gather#217 step 3/4 (+495ns): 434ns self time
                                                            Gather#217 step 4/4 (+929ns): return nil
                                                            Gather#217 ends at 331.71µs
                                                        Gather#218 step 3/4 (+399ns): 403ns self time
                                                        Gather#218 step 4/4 (+802ns): return nil
                                                        Gather#218 ends at 321.187µs
                                                    Gather#220 step 3/4 (+498ns): 497ns self time
                                                    Gather#220 step 4/4 (+995ns): return nil
                                                    Gather#220 ends at 310.885µs
                                                Combine#222 step 5/8 (+1.954µs): 981ns self time
                                                Combine#222 step 6/8 (+2.935µs): scatter:
                                                  Task#221: pool=5
                                                  Task#221 step 1/2 (+0s): 2.063µs self time
                                                  Task#221 step 2/2 (+2.063µs): return nil
                                                  Task#221 ends at 302.935µs
                                                    Combine#221: index=2 flush=<nil>
                                                    Combine#221 step 1/6 (+0s): 345ns self time
                                                    Combine#221 step 2/6 (+345ns): scatter:
                                                      Task#214: pool=3
                                                      Task#214 step 1/2 (+0s): 9.219µs self time
                                                      Task#214 step 2/2 (+9.219µs): return nil
                                                      Task#214 ends at 312.499µs
                                                        Gather#214: index=0
                                                        Gather#214 step 1/2 (+0s): 1.006µs self time
                                                        Gather#214 step 2/2 (+1.006µs): return nil
                                                        Gather#214 ends at 313.505µs
                                                    Combine#221 step 3/6 (+345ns): 341ns self time
                                                    Combine#221 step 4/6 (+686ns): scatter:
                                                      Task#219: pool=4
                                                      Task#219 step 1/2 (+0s): 9.982µs self time
                                                      Task#219 step 2/2 (+9.982µs): return nil
                                                      Task#219 ends at 313.603µs
                                                        Combine#219: index=5 flush=<nil>
                                                        Combine#219 step 1/4 (+0s): 493ns self time
                                                        Combine#219 step 2/4 (+493ns): scatter:
                                                          Task#216: pool=1
                                                          Task#216 step 1/2 (+0s): 9.981µs self time
                                                          Task#216 step 2/2 (+9.981µs): return nil
                                                          Task#216 ends at 324.077µs
                                                            Gather#216: index=5
                                                            Gather#216 step 1/2 (+0s): 191ns self time
                                                            Gather#216 step 2/2 (+191ns): return nil
                                                            Gather#216 ends at 324.268µs
                                                        Combine#219 step 3/4 (+493ns): 501ns self time
                                                        Combine#219 step 4/4 (+994ns): return nil
                                                        Combine#219 ends at 314.597µs
                                                    Combine#221 step 5/6 (+686ns): 347ns self time
                                                    Combine#221 step 6/6 (+1.033µs): return nil
                                                    Combine#221 ends at 303.968µs
                                                Combine#222 step 7/8 (+2.935µs): 958ns self time
                                                Combine#222 step 8/8 (+3.893µs): return nil
                                                Combine#222 ends at 301.83µs
                                            Gather#223 step 3/6 (+286.526µs): 286.529µs self time
                                            Gather#223 step 4/6 (+573.055µs): scatter:
                                              Task#215: pool=1
                                              Task#215 step 1/2 (+0s): 10.001µs self time
                                              Task#215 step 2/2 (+10.001µs): return nil
                                              Task#215 ends at 593.052µs
                                                Gather#215: index=0
                                                Gather#215 step 1/2 (+0s): 712ns self time
                                                Gather#215 step 2/2 (+712ns): return nil
                                                Gather#215 ends at 593.764µs
                                            Gather#223 step 5/6 (+573.055µs): 286.531µs self time
                                            Gather#223 step 6/6 (+859.586µs): return nil
                                            Gather#223 ends at 869.582µs
                                        Plan#10 step 2/2 (+0s): ends at 869.582µs
                                      Gather#211 step 3/4 (+870.078µs): 507ns self time
                                      Gather#211 step 4/4 (+870.585µs): return nil
                                      Gather#211 ends at 906.947µs
                                  Combine#264 step 3/6 (+303ns): 546ns self time
                                  Combine#264 step 4/6 (+849ns): scatter:
                                    Task#225: pool=0
                                    Task#225 step 1/2 (+0s): 9.994µs self time
                                    Task#225 step 2/2 (+9.994µs): return nil
                                    Task#225 ends at 30.843µs
                                      Gather#225: index=11
                                      Gather#225 step 1/2 (+0s): 1.017µs self time
                                      Gather#225 step 2/2 (+1.017µs): return nil
                                      Gather#225 ends at 31.86µs
                                  Combine#264 step 5/6 (+849ns): 149ns self time
                                  Combine#264 step 6/6 (+998ns): return nil
                                  Combine#264 ends at 20.998µs
                              Gather#268 step 5/8 (+0s): 9ns self time
                              Gather#268 step 6/8 (+9ns): scatter:
                                Task#203: pool=1
                                Task#203 step 1/2 (+0s): 11.695µs self time
                                Task#203 step 2/2 (+11.695µs): return nil
                                Task#203 ends at 21.707µs
                                  Combine#203: index=1 flush=<nil>
                                  Combine#203 step 1/2 (+0s): 1.086µs self time
                                  Combine#203 step 2/2 (+1.086µs): return nil
                                  Combine#203 ends at 22.793µs
                              Gather#268 step 7/8 (+9ns): 9ns self time
                              Gather#268 step 8/8 (+18ns): return nil
                              Gather#268 ends at 10.021µs
                          Plan#9 step 2/5 (+0s): scatter:
                            Task#265: pool=1
                            Task#265 step 1/2 (+0s): 10.005µs self time
                            Task#265 step 2/2 (+10.005µs): return nil
                            Task#265 ends at 10.005µs
                              Gather#265: index=0
                              Gather#265 step 1/4 (+0s): 469ns self time
                              Gather#265 step 2/4 (+469ns): scatter:
                                Task#263: pool=0
                                Task#263 step 1/2 (+0s): 6.767µs self time
                                Task#263 step 2/2 (+6.767µs): return nil
                                Task#263 ends at 17.241µs
                                  Gather#263: index=6
                                  Gather#263 step 1/14 (+0s): 65ns self time
                                  Gather#263 step 2/14 (+65ns): scatter:
                                    Task#228: pool=0
                                    Task#228 step 1/2 (+0s): 10.002µs self time
                                    Task#228 step 2/2 (+10.002µs): return nil
                                    Task#228 ends at 27.308µs
                                      Gather#228: index=5
                                      Gather#228 step 1/2 (+0s): 944ns self time
                                      Gather#228 step 2/2 (+944ns): return nil
                                      Gather#228 ends at 28.252µs
                                  Gather#263 step 3/14 (+65ns): 45ns self time
                                  Gather#263 step 4/14 (+110ns): scatter:
                                    Task#206: pool=0
                                    Task#206 step 1/2 (+0s): 10.197µs self time
                                    Task#206 step 2/2 (+10.197µs): return nil
                                    Task#206 ends at 27.548µs
                                      Gather#206: index=6
                                      Gather#206 step 1/2 (+0s): 676ns self time
                                      Gather#206 step 2/2 (+676ns): return nil
                                      Gather#206 ends at 28.224µs
                                  Gather#263 step 5/14 (+110ns): 23ns self time
                                  Gather#263 step 6/14 (+133ns): scatter:
                                    Task#226: pool=0
                                    Task#226 step 1/2 (+0s): 57.199µs self time
                                    Task#226 step 2/2 (+57.199µs): return nil
                                    Task#226 ends at 74.573µs
                                      Gather#226: index=2
                                      Gather#226 step 1/2 (+0s): 95ns self time
                                      Gather#226 step 2/2 (+95ns): return nil
                                      Gather#226 ends at 74.668µs
                                  Gather#263 step 7/14 (+133ns): 62ns self time
                                  Gather#263 step 8/14 (+195ns): scatter:
                                    Task#209: pool=1
                                    Task#209 step 1/2 (+0s): 9.688µs self time
                                    Task#209 step 2/2 (+9.688µs): return nil
                                    Task#209 ends at 27.124µs
                                      Gather#209: index=6
                                      Gather#209 step 1/2 (+0s): 1.001µs self time
                                      Gather#209 step 2/2 (+1.001µs): return nil
                                      Gather#209 ends at 28.125µs
                                  Gather#263 step 9/14 (+195ns): 75ns self time
                                  Gather#263 step 10/14 (+270ns): scatter:
                                    Task#234: pool=1
                                    Task#234 step 1/4 (+0s): 5.224µs self time
                                    Task#234 step 2/4 (+5.224µs): subjob:
                                      Plan#11: pathCount=16 taskCount=28 maxPathDuration=10.044874ms minGatherCount=21 maxGatherCount=48
                                         TaskPools[0]: TaskPool#59: limit=6
                                         CombinerPools[0]: CombinerPool#44: limit=5
                                         CombinerPools[1]: CombinerPool#45: limit=1
                                         CombinerPools[2]: CombinerPool#46: limit=6
                                         CombinerPools[3]: CombinerPool#47: limit=4
                                         CombinerPools[4]: CombinerPool#48: limit=3
                                         CombinerPools[5]: CombinerPool#49: limit=4
                                         CombinerPools[6]: CombinerPool#50: limit=10
                                         CombinerPools[7]: CombinerPool#51: limit=1
                                         CombinerPools[8]: CombinerPool#52: limit=6
                                         Combiners[0]: pool=8
                                         Combiners[1]: pool=6
                                         Combiners[2]: pool=7
                                         Combiners[3]: pool=0
                                         Combiners[4]: pool=0
                                         Combiners[5]: pool=0
                                         Combiners[6]: pool=1
                                         Combiners[7]: pool=7
                                         Combiners[8]: pool=2
                                         Combiners[9]: pool=5
                                         Combiners[10]: pool=6
                                         Combiners[11]: pool=6
                                         Combiners[12]: pool=0
                                         Combiners[13]: pool=1
                                         Combiners[14]: pool=6
                                         Combiners[15]: pool=4
                                         Combiners[16]: pool=2
                                         Combiners[17]: pool=7
                                         Combiners[18]: pool=1
                                         Combiners[19]: pool=5
                                      Plan#11 step 1/4 (+0s): scatter:
                                        Task#261: pool=0
                                        Task#261 step 1/2 (+0s): 35.979µs self time
                                        Task#261 step 2/2 (+35.979µs): return nil
                                        Task#261 ends at 35.979µs
                                          Combine#261: index=9 flush=<nil>
                                          Combine#261 step 1/4 (+0s): 2ns self time
                                          Combine#261 step 2/4 (+2ns): scatter:
                                            Task#240: pool=0
                                            Task#240 step 1/2 (+0s): 11.682µs self time
                                            Task#240 step 2/2 (+11.682µs): return nil
                                            Task#240 ends at 47.663µs
                                              Combine#240: index=3 flush=<nil>
                                              Combine#240 step 1/2 (+0s): 11.968µs self time
                                              Combine#240 step 2/2 (+11.968µs): return nil
                                              Combine#240 ends at 59.631µs
                                          Combine#261 step 3/4 (+2ns): 62ns self time
                                          Combine#261 step 4/4 (+64ns): return nil
                                          Combine#261 ends at 36.043µs
                                      Plan#11 step 2/4 (+0s): scatter:
                                        Task#260: pool=0
                                        Task#260 step 1/2 (+0s): 10.321µs self time
                                        Task#260 step 2/2 (+10.321µs): return nil
                                        Task#260 ends at 10.321µs
                                          Combine#260: index=0 flush=<nil>
                                          Combine#260 step 1/4 (+0s): 644.639µs self time
                                          Combine#260 step 2/4 (+644.639µs): scatter:
                                            Task#236: pool=0
                                            Task#236 step 1/2 (+0s): 10.112µs self time
                                            Task#236 step 2/2 (+10.112µs): return nil
                                            Task#236 ends at 665.072µs
                                              Gather#236: index=7
                                              Gather#236 step 1/2 (+0s): 976ns self time
                                              Gather#236 step 2/2 (+976ns): return nil
                                              Gather#236 ends at 666.048µs
                                          Combine#260 step 3/4 (+644.639µs): 163.958µs self time
                                          Combine#260 step 4/4 (+808.597µs): return nil
                                          Combine#260 ends at 818.918µs
                                      Plan#11 step 3/4 (+0s): scatter:
                                        Task#262: pool=0
                                        Task#262 step 1/2 (+0s): 8.978µs self time
                                        Task#262 step 2/2 (+8.978µs): return nil
                                        Task#262 ends at 8.978µs
                                          Gather#262: index=6
                                          Gather#262 step 1/8 (+0s): 451ns self time
                                          Gather#262 step 2/8 (+451ns): scatter:
                                            Task#250: pool=0
                                            Task#250 step 1/2 (+0s): 6.866µs self time
                                            Task#250 step 2/2 (+6.866µs): return nil
                                            Task#250 ends at 16.295µs
                                              Gather#250: index=4
                                              Gather#250 step 1/2 (+0s): 21.714µs self time
                                              Gather#250 step 2/2 (+21.714µs): return nil
                                              Gather#250 ends at 38.009µs
                                          Gather#262 step 3/8 (+451ns): 163ns self time
                                          Gather#262 step 4/8 (+614ns): scatter:
                                            Task#249: pool=0
                                            Task#249 step 1/2 (+0s): 10.002µs self time
                                            Task#249 step 2/2 (+10.002µs): return nil
                                            Task#249 ends at 19.594µs
                                              Gather#249: index=17
                                              Gather#249 step 1/2 (+0s): 1.006µs self time
                                              Gather#249 step 2/2 (+1.006µs): return nil
                                              Gather#249 ends at 20.6µs
                                          Gather#262 step 5/8 (+614ns): 194ns self time
                                          Gather#262 step 6/8 (+808ns): scatter:
                                            Task#259: pool=0
                                            Task#259 step 1/2 (+0s): 5.926µs self time
                                            Task#259 step 2/2 (+5.926µs): return nil
                                            Task#259 ends at 15.712µs
                                              Gather#259: index=13
                                              Gather#259 step 1/14 (+0s): 907ns self time
                                              Gather#259 step 2/14 (+907ns): scatter:
                                                Task#257: pool=0
                                                Task#257 step 1/2 (+0s): 10.968µs self time
                                                Task#257 step 2/2 (+10.968µs): return nil
                                                Task#257 ends at 27.587µs
                                                  Combine#257: index=17 flush=<nil>
                                                  Combine#257 step 1/4 (+0s): 553ns self time
                                                  Combine#257 step 2/4 (+553ns): scatter:
                                                    Task#255: pool=0
                                                    Task#255 step 1/2 (+0s): 9.998µs self time
                                                    Task#255 step 2/2 (+9.998µs): return nil
                                                    Task#255 ends at 38.138µs
                                                      Gather#255: index=10
                                                      Gather#255 step 1/6 (+0s): 999ns self time
                                                      Gather#255 step 2/6 (+999ns): scatter:
                                                        Task#246: pool=0
                                                        Task#246 step 1/2 (+0s): 9.988µs self time
                                                        Task#246 step 2/2 (+9.988µs): return nil
                                                        Task#246 ends at 49.125µs
                                                          Gather#246: index=16
                                                          Gather#246 step 1/2 (+0s): 0s self time
                                                          Gather#246 step 2/2 (+0s): return nil
                                                          Gather#246 ends at 49.125µs
                                                      Gather#255 step 3/6 (+999ns): 0s self time
                                                      Gather#255 step 4/6 (+999ns): scatter:
                                                        Task#251: pool=0
                                                        Task#251 step 1/2 (+0s): 10.002µs self time
                                                        Task#251 step 2/2 (+10.002µs): return nil
                                                        Task#251 ends at 49.139µs
                                                          Gather#251: index=3
                                                          Gather#251 step 1/4 (+0s): 40ns self time
                                                          Gather#251 step 2/4 (+40ns): scatter:
                                                            Task#237: pool=0
                                                            Task#237 step 1/2 (+0s): 9.832µs self time
                                                            Task#237 step 2/2 (+9.832µs): return nil
                                                            Task#237 ends at 59.011µs
                                                              Combine#237: index=18 flush=<nil>
                                                              Combine#237 step 1/2 (+0s): 999ns self time
                                                              Combine#237 step 2/2 (+999ns): return nil
                                                              Combine#237 ends at 60.01µs
                                                          Gather#251 step 3/4 (+40ns): 850ns self time
                                                          Gather#251 step 4/4 (+890ns): return nil
                                                          Gather#251 ends at 50.029µs
                                                      Gather#255 step 5/6 (+999ns): 6ns self time
                                                      Gather#255 step 6/6 (+1.005µs): return nil
                                                      Gather#255 ends at 39.143µs
                                                  Combine#257 step 3/4 (+553ns): 460ns self time
                                                  Combine#257 step 4/4 (+1.013µs): return nil
                                                  Combine#257 ends at 28.6µs
                                              Gather#259 step 3/14 (+907ns): 0s self time
                                              Gather#259 step 4/14 (+907ns): scatter:
                                                Task#244: pool=0
                                                Task#244 step 1/2 (+0s): 9.914µs self time
                                                Task#244 step 2/2 (+9.914µs): return nil
                                                Task#244 ends at 26.533µs
                                                  Gather#244: index=3
                                                  Gather#244 step 1/2 (+0s): 1.118µs self time
                                                  Gather#244 step 2/2 (+1.118µs): return nil
                                                  Gather#244 ends at 27.651µs
                                              Gather#259 step 5/14 (+907ns): 0s self time
                                              Gather#259 step 6/14 (+907ns): scatter:
                                                Task#242: pool=0
                                                Task#242 step 1/2 (+0s): 10µs self time
                                                Task#242 step 2/2 (+10µs): return nil
                                                Task#242 ends at 26.619µs
                                                  Combine#242: index=3 flush=<nil>
                                                  Combine#242 step 1/2 (+0s): 969ns self time
                                                  Combine#242 step 2/2 (+969ns): return nil
                                                  Combine#242 ends at 27.588µs
                                              Gather#259 step 7/14 (+907ns): 0s self time
                                              Gather#259 step 8/14 (+907ns): scatter:
                                                Task#256: pool=0
                                                Task#256 step 1/2 (+0s): 5.003725ms self time
                                                Task#256 step 2/2 (+5.003725ms): return nil
                                                Task#256 ends at 5.020344ms
                                                  Gather#256: index=18
                                                  Gather#256 step 1/4 (+0s): 502ns self time
                                                  Gather#256 step 2/4 (+502ns): scatter:
                                                    Task#239: pool=0
                                                    Task#239 step 1/2 (+0s): 9.998µs self time
                                                    Task#239 step 2/2 (+9.998µs): return nil
                                                    Task#239 ends at 5.030844ms
                                                      Gather#239: index=5
                                                      Gather#239 step 1/2 (+0s): 102.701µs self time
                                                      Gather#239 step 2/2 (+102.701µs): return nil
                                                      Gather#239 ends at 5.133545ms
                                                  Gather#256 step 3/4 (+502ns): 511ns self time
                                                  Gather#256 step 4/4 (+1.013µs): return nil
                                                  Gather#256 ends at 5.021357ms
                                              Gather#259 step 9/14 (+907ns): 0s self time
                                              Gather#259 step 10/14 (+907ns): scatter:
                                                Task#238: pool=0
                                                Task#238 step 1/2 (+0s): 10µs self time
                                                Task#238 step 2/2 (+10µs): return nil
                                                Task#238 ends at 26.619µs
                                                  Combine#238: index=4 flush=<nil>
                                                  Combine#238 step 1/2 (+0s): 27.624µs self time
                                                  Combine#238 step 2/2 (+27.624µs): return nil
                                                  Combine#238 ends at 54.243µs
                                              Gather#259 step 11/14 (+907ns): 0s self time
                                              Gather#259 step 12/14 (+907ns): scatter:
                                                Task#258: pool=0
                                                Task#258 step 1/2 (+0s): 5.449µs self time
                                                Task#258 step 2/2 (+5.449µs): return nil
                                                Task#258 ends at 22.068µs
                                                  Gather#258: index=2
                                                  Gather#258 step 1/4 (+0s): 500ns self time
                                                  Gather#258 step 2/4 (+500ns): scatter:
                                                    Task#254: pool=0
                                                    Task#254 step 1/2 (+0s): 10ms self time
                                                    Task#254 step 2/2 (+10ms): return nil
                                                    Task#254 ends at 10.022568ms
                                                      Gather#254: index=3
                                                      Gather#254 step 1/12 (+0s): 119ns self time
                                                      Gather#254 step 2/12 (+119ns): scatter:
                                                        Task#235: pool=0
                                                        Task#235 step 1/2 (+0s): 10µs self time
                                                        Task#235 step 2/2 (+10µs): return nil
                                                        Task#235 ends at 10.032687ms
                                                          Gather#235: index=1
                                                          Gather#235 step 1/2 (+0s): 1µs self time
                                                          Gather#235 step 2/2 (+1µs): return nil
                                                          Gather#235 ends at 10.033687ms
                                                      Gather#254 step 3/12 (+119ns): 486ns self time
                                                      Gather#254 step 4/12 (+605ns): scatter:
                                                        Task#243: pool=0
                                                        Task#243 step 1/2 (+0s): 9.978µs self time
                                                        Task#243 step 2/2 (+9.978µs): return nil
                                                        Task#243 ends at 10.033151ms
                                                          Gather#243: index=14
                                                          Gather#243 step 1/2 (+0s): 879ns self time
                                                          Gather#243 step 2/2 (+879ns): return nil
                                                          Gather#243 ends at 10.03403ms
                                                      Gather#254 step 5/12 (+605ns): 21ns self time
                                                      Gather#254 step 6/12 (+626ns): scatter:
                                                        Task#252: pool=0
                                                        Task#252 step 1/2 (+0s): 9.999µs self time
                                                        Task#252 step 2/2 (+9.999µs): return nil
                                                        Task#252 ends at 10.033193ms
                                                          Gather#252: index=19
                                                          Gather#252 step 1/6 (+0s): 345ns self time
                                                          Gather#252 step 2/6 (+345ns): scatter:
                                                            Task#241: pool=0
                                                            Task#241 step 1/2 (+0s): 10.013µs self time
                                                            Task#241 step 2/2 (+10.013µs): return nil
                                                            Task#241 ends at 10.043551ms
                                                              Gather#241: index=11
                                                              Gather#241 step 1/2 (+0s): 1.034µs self time
                                                              Gather#241 step 2/2 (+1.034µs): return nil
                                                              Gather#241 ends at 10.044585ms
                                                          Gather#252 step 3/6 (+345ns): 337ns self time
                                                          Gather#252 step 4/6 (+682ns): scatter:
                                                            Task#248: pool=0
                                                            Task#248 step 1/2 (+0s): 9.997µs self time
                                                            Task#248 step 2/2 (+9.997µs): return nil
                                                            Task#248 ends at 10.043872ms
                                                              Gather#248: index=2
                                                              Gather#248 step 1/2 (+0s): 1.002µs self time
                                                              Gather#248 step 2/2 (+1.002µs): return nil
                                                              Gather#248 ends at 10.044874ms
                                                          Gather#252 step 5/6 (+682ns): 320ns self time
                                                          Gather#252 step 6/6 (+1.002µs): return nil
                                                          Gather#252 ends at 10.034195ms
                                                      Gather#254 step 7/12 (+626ns): 29ns self time
                                                      Gather#254 step 8/12 (+655ns): scatter:
                                                        Task#253: pool=0
                                                        Task#253 step 1/2 (+0s): 0s self time
                                                        Task#253 step 2/2 (+0s): return nil
                                                        Task#253 ends at 10.023223ms
                                                          Gather#253: index=1
                                                          Gather#253 step 1/4 (+0s): 497ns self time
                                                          Gather#253 step 2/4 (+497ns): scatter:
                                                            Task#245: pool=0
                                                            Task#245 step 1/2 (+0s): 9.999µs self time
                                                            Task#245 step 2/2 (+9.999µs): return nil
                                                            Task#245 ends at 10.033719ms
                                                              Gather#245: index=17
                                                              Gather#245 step 1/2 (+0s): 831ns self time
                                                              Gather#245 step 2/2 (+831ns): return nil
                                                              Gather#245 ends at 10.03455ms
                                                          Gather#253 step 3/4 (+497ns): 500ns self time
                                                          Gather#253 step 4/4 (+997ns): return nil
                                                          Gather#253 ends at 10.02422ms
                                                      Gather#254 step 9/12 (+655ns): 24ns self time
                                                      Gather#254 step 10/12 (+679ns): scatter:
                                                        Task#247: pool=0
                                                        Task#247 step 1/2 (+0s): 9.998µs self time
                                                        Task#247 step 2/2 (+9.998µs): return nil
                                                        Task#247 ends at 10.033245ms
                                                          Gather#247: index=12
                                                          Gather#247 step 1/2 (+0s): 996ns self time
                                                          Gather#247 step 2/2 (+996ns): return nil
                                                          Gather#247 ends at 10.034241ms
                                                      Gather#254 step 11/12 (+679ns): 16ns self time
                                                      Gather#254 step 12/12 (+695ns): return nil
                                                      Gather#254 ends at 10.023263ms
                                                  Gather#258 step 3/4 (+500ns): 504ns self time
                                                  Gather#258 step 4/4 (+1.004µs): return nil
                                                  Gather#258 ends at 23.072µs
                                              Gather#259 step 13/14 (+907ns): 0s self time
                                              Gather#259 step 14/14 (+907ns): return nil
                                              Gather#259 ends at 16.619µs
                                          Gather#262 step 7/8 (+808ns): 95ns self time
                                          Gather#262 step 8/8 (+903ns): return nil
                                          Gather#262 ends at 9.881µs
                                      Plan#11 step 4/4 (+0s): ends at 10.044874ms
                                    Task#234 step 3/4 (+10.050098ms): 4.768µs self time
                                    Task#234 step 4/4 (+10.054866ms): return nil
                                    Task#234 ends at 10.072377ms
                                      Gather#234: index=0
                                      Gather#234 step 1/6 (+0s): 455ns self time
                                      Gather#234 step 2/6 (+455ns): scatter:
                                        Task#232: pool=1
                                        Task#232 step 1/2 (+0s): 8.284µs self time
                                        Task#232 step 2/2 (+8.284µs): return nil
                                        Task#232 ends at 10.081116ms
                                          Combine#232: index=0 flush=<nil>
                                          Combine#232 step 1/6 (+0s): 309ns self time
                                          Combine#232 step 2/6 (+309ns): scatter:
                                            Task#224: pool=0
                                            Task#224 step 1/2 (+0s): 73.104µs self time
                                            Task#224 step 2/2 (+73.104µs): return nil
                                            Task#224 ends at 10.154529ms
                                              Gather#224: index=3
                                              Gather#224 step 1/2 (+0s): 1.002µs self time
                                              Gather#224 step 2/2 (+1.002µs): return nil
                                              Gather#224 ends at 10.155531ms
                                          Combine#232 step 3/6 (+309ns): 0s self time
                                          Combine#232 step 4/6 (+309ns): scatter:
                                            Task#230: pool=1
                                            Task#230 step 1/2 (+0s): 146.307µs self time
                                            Task#230 step 2/2 (+146.307µs): return nil
                                            Task#230 ends at 10.227732ms
                                              Gather#230: index=6
                                              Gather#230 step 1/4 (+0s): 534ns self time
                                              Gather#230 step 2/4 (+534ns): scatter:
                                                Task#208: pool=0
                                                Task#208 step 1/2 (+0s): 9.997µs self time
                                                Task#208 step 2/2 (+9.997µs): return error
                                                Task#208 ends at 10.238263ms
                                                  Gather#208: index=0
                                                  Gather#208 step 1/2 (+0s): 771ns self time
                                                  Gather#208 step 2/2 (+771ns): return nil
                                                  Gather#208 ends at 10.239034ms
                                              Gather#230 step 3/4 (+534ns): 336ns self time
                                              Gather#230 step 4/4 (+870ns): return nil
                                              Gather#230 ends at 10.228602ms
                                          Combine#232 step 5/6 (+309ns): 705ns self time
                                          Combine#232 step 6/6 (+1.014µs): return nil
                                          Combine#232 ends at 10.08213ms
                                      Gather#234 step 3/6 (+455ns): 138ns self time
                                      Gather#234 step 4/6 (+593ns): scatter:
                                        Task#231: pool=0
                                        Task#231 step 1/2 (+0s): 4.751336ms self time
                                        Task#231 step 2/2 (+4.751336ms): return nil
                                        Task#231 ends at 14.824306ms
                                          Gather#231: index=8
                                          Gather#231 step 1/4 (+0s): 0s self time
                                          Gather#231 step 2/4 (+0s): scatter:
                                            Task#229: pool=1
                                            Task#229 step 1/2 (+0s): 9.992µs self time
                                            Task#229 step 2/2 (+9.992µs): return nil
                                            Task#229 ends at 14.834298ms
                                              Gather#229: index=0
                                              Gather#229 step 1/4 (+0s): 483ns self time
                                              Gather#229 step 2/4 (+483ns): scatter:
                                                Task#207: pool=0
                                                Task#207 step 1/2 (+0s): 9.98µs self time
                                                Task#207 step 2/2 (+9.98µs): return nil
                                                Task#207 ends at 14.844761ms
                                                  Gather#207: index=7
                                                  Gather#207 step 1/2 (+0s): 10.852µs self time
                                                  Gather#207 step 2/2 (+10.852µs): return nil
                                                  Gather#207 ends at 14.855613ms
                                              Gather#229 step 3/4 (+483ns): 520ns self time
                                              Gather#229 step 4/4 (+1.003µs): return nil
                                              Gather#229 ends at 14.835301ms
                                          Gather#231 step 3/4 (+0s): 1.151µs self time
                                          Gather#231 step 4/4 (+1.151µs): return nil
                                          Gather#231 ends at 14.825457ms
                                      Gather#234 step 5/6 (+593ns): 403ns self time
                                      Gather#234 step 6/6 (+996ns): return nil
                                      Gather#234 ends at 10.073373ms
                                  Gather#263 step 11/14 (+270ns): 10ns self time
                                  Gather#263 step 12/14 (+280ns): scatter:
                                    Task#233: pool=0
                                    Task#233 step 1/2 (+0s): 10.101µs self time
                                    Task#233 step 2/2 (+10.101µs): return nil
                                    Task#233 ends at 27.622µs
                                      Gather#233: index=11
                                      Gather#233 step 1/4 (+0s): 402.121µs self time
                                      Gather#233 step 2/4 (+402.121µs): scatter:
                                        Task#227: pool=1
                                        Task#227 step 1/2 (+0s): 10.001µs self time
                                        Task#227 step 2/2 (+10.001µs): return nil
                                        Task#227 ends at 439.744µs
                                          Combine#227: index=1 flush=<nil>
                                          Combine#227 step 1/2 (+0s): 2.297µs self time
                                          Combine#227 step 2/2 (+2.297µs): return error
                                          Combine#227 ends at 442.041µs
                                      Gather#233 step 3/4 (+402.121µs): 402.411µs self time
                                      Gather#233 step 4/4 (+804.532µs): return error
                                      Gather#233 ends at 832.154µs
                                  Gather#263 step 13/14 (+280ns): 133ns self time
                                  Gather#263 step 14/14 (+413ns): return nil
                                  Gather#263 ends at 17.654µs
                              Gather#265 step 3/4 (+469ns): 528ns self time
                              Gather#265 step 4/4 (+997ns): return nil
                              Gather#265 ends at 11.002µs
                          Plan#9 step 3/5 (+0s): scatter:
                            Task#266: pool=0
                            Task#266 step 1/2 (+0s): 564.333µs self time
                            Task#266 step 2/2 (+564.333µs): return nil
                            Task#266 ends at 564.333µs
                              Combine#266: index=0 flush=<nil>
                              Combine#266 step 1/4 (+0s): 523ns self time
                              Combine#266 step 2/4 (+523ns): scatter:
                                Task#205: pool=0
                                Task#205 step 1/2 (+0s): 10µs self time
                                Task#205 step 2/2 (+10µs): return nil
                                Task#205 ends at 574.856µs
                                  Combine#205: index=0 flush=<nil>
                                  Combine#205 step 1/2 (+0s): 1µs self time
                                  Combine#205 step 2/2 (+1µs): return nil
                                  Combine#205 ends at 575.856µs
                              Combine#266 step 3/4 (+523ns): 553ns self time
                              Combine#266 step 4/4 (+1.076µs): return nil
                              Combine#266 ends at 565.409µs
                          Plan#9 step 4/5 (+0s): scatter:
                            Task#267: pool=1
                            Task#267 step 1/2 (+0s): 9.994µs self time
                            Task#267 step 2/2 (+9.994µs): return nil
                            Task#267 ends at 9.994µs
                              Gather#267: index=4
                              Gather#267 step 1/4 (+0s): 83.618µs self time
                              Gather#267 step 2/4 (+83.618µs): scatter:
                                Task#210: pool=1
                                Task#210 step 1/2 (+0s): 9.999µs self time
                                Task#210 step 2/2 (+9.999µs): return nil
                                Task#210 ends at 103.611µs
                                  Gather#210: index=0
                                  Gather#210 step 1/2 (+0s): 998ns self time
                                  Gather#210 step 2/2 (+998ns): return nil
                                  Gather#210 ends at 104.609µs
                              Gather#267 step 3/4 (+83.618µs): 83.626µs self time
                              Gather#267 step 4/4 (+167.244µs): return nil
                              Gather#267 ends at 177.238µs
                          Plan#9 step 5/5 (+0s): ends at 14.855613ms
                        Task#202 step 3/4 (+14.865597ms): 0s self time
                        Task#202 step 4/4 (+14.865597ms): return nil
                        Task#202 ends at 25.141862ms
                          Gather#202: index=3
                          Gather#202 step 1/4 (+0s): 492ns self time
                          Gather#202 step 2/4 (+492ns): scatter:
                            Task#199: pool=1
                            Task#199 step 1/2 (+0s): 9.998µs self time
                            Task#199 step 2/2 (+9.998µs): return nil
                            Task#199 ends at 25.152352ms
                              Gather#199: index=3
                              Gather#199 step 1/6 (+0s): 320ns self time
                              Gather#199 step 2/6 (+320ns): scatter:
                                Task#131: pool=4
                                Task#131 step 1/2 (+0s): 9.999µs self time
                                Task#131 step 2/2 (+9.999µs): return nil
                                Task#131 ends at 25.162671ms
                                  Combine#131: index=0 flush=<nil>
                                  Combine#131 step 1/2 (+0s): 997ns self time
                                  Combine#131 step 2/2 (+997ns): return nil
                                  Combine#131 ends at 25.163668ms
                              Gather#199 step 3/6 (+320ns): 344ns self time
                              Gather#199 step 4/6 (+664ns): scatter:
                                Task#126: pool=4
                                Task#126 step 1/2 (+0s): 1.627251ms self time
                                Task#126 step 2/2 (+1.627251ms): return nil
                                Task#126 ends at 26.780267ms
                                  Gather#126: index=1
                                  Gather#126 step 1/2 (+0s): 1µs self time
                                  Gather#126 step 2/2 (+1µs): return nil
                                  Gather#126 ends at 26.781267ms
                              Gather#199 step 5/6 (+664ns): 333ns self time
                              Gather#199 step 6/6 (+997ns): return nil
                              Gather#199 ends at 25.153349ms
                          Gather#202 step 3/4 (+492ns): 493ns self time
                          Gather#202 step 4/4 (+985ns): return nil
                          Gather#202 ends at 25.142847ms
                      Gather#272 step 5/6 (+606ns): 392ns self time
                      Gather#272 step 6/6 (+998ns): return nil
                      Gather#272 ends at 10.276657ms
                  Gather#273 step 7/12 (+717ns): 276ns self time
                  Gather#273 step 8/12 (+993ns): scatter:
                    Task#270: pool=8
                    Task#270 step 1/2 (+0s): 10.015µs self time
                    Task#270 step 2/2 (+10.015µs): return error
                    Task#270 ends at 10.275895ms
                      Combine#270: index=1 flush=<nil>
                      Combine#270 step 1/4 (+0s): 521ns self time
                      Combine#270 step 2/4 (+521ns): scatter:
                        Task#201: pool=1
                        Task#201 step 1/2 (+0s): 10.001µs self time
                        Task#201 step 2/2 (+10.001µs): return nil
                        Task#201 ends at 10.286417ms
                          Gather#201: index=3
                          Gather#201 step 1/4 (+0s): 265ns self time
                          Gather#201 step 2/4 (+265ns): scatter:
                            Task#200: pool=9
                            Task#200 step 1/2 (+0s): 10.003µs self time
                            Task#200 step 2/2 (+10.003µs): return nil
                            Task#200 ends at 10.296685ms
                              Combine#200: index=3 flush=<nil>
                              Combine#200 step 1/4 (+0s): 268ns self time
                              Combine#200 step 2/4 (+268ns): scatter:
                                Task#4: pool=4
                                Task#4 step 1/2 (+0s): 12.339µs self time
                                Task#4 step 2/2 (+12.339µs): return nil
                                Task#4 ends at 10.309292ms
                                  Gather#4: index=2
                                  Gather#4 step 1/2 (+0s): 953ns self time
                                  Gather#4 step 2/2 (+953ns): return nil
                                  Gather#4 ends at 10.310245ms
                              Combine#200 step 3/4 (+268ns): 273ns self time
                              Combine#200 step 4/4 (+541ns): return nil
                              Combine#200 ends at 10.297226ms
                          Gather#201 step 3/4 (+265ns): 733ns self time
                          Gather#201 step 4/4 (+998ns): return nil
                          Gather#201 ends at 10.287415ms
                      Combine#270 step 3/4 (+521ns): 544ns self time
                      Combine#270 step 4/4 (+1.065µs): return nil
                      Combine#270 ends at 10.27696ms
                  Gather#273 step 9/12 (+993ns): 0s self time
                  Gather#273 step 10/12 (+993ns): scatter:
                    Task#127: pool=4
                    Task#127 step 1/2 (+0s): 0s self time
                    Task#127 step 2/2 (+0s): return nil
                    Task#127 ends at 10.26588ms
                      Combine#127: index=0 flush=<nil>
                      Combine#127 step 1/2 (+0s): 998ns self time
                      Combine#127 step 2/2 (+998ns): return nil
                      Combine#127 ends at 10.266878ms
                  Gather#273 step 11/12 (+993ns): 0s self time
                  Gather#273 step 12/12 (+993ns): return error
                  Gather#273 ends at 10.26588ms
              Gather#331 step 3/10 (+127ns): 82ns self time
              Gather#331 step 4/10 (+209ns): scatter:
                Task#70: pool=1
                Task#70 step 1/2 (+0s): 9.999µs self time
                Task#70 step 2/2 (+9.999µs): return nil
                Task#70 ends at 20.218µs
                  Combine#70: index=3 flush=<nil>
                  Combine#70 step 1/2 (+0s): 1.125µs self time
                  Combine#70 step 2/2 (+1.125µs): return nil
                  Combine#70 ends at 21.343µs
              Gather#331 step 5/10 (+209ns): 283ns self time
              Gather#331 step 6/10 (+492ns): scatter:
                Task#130: pool=1
                Task#130 step 1/2 (+0s): 6.771235ms self time
                Task#130 step 2/2 (+6.771235ms): return nil
                Task#130 ends at 6.781737ms
                  Gather#130: index=1
                  Gather#130 step 1/2 (+0s): 997ns self time
                  Gather#130 step 2/2 (+997ns): return nil
                  Gather#130 ends at 6.782734ms
              Gather#331 step 7/10 (+492ns): 248ns self time
              Gather#331 step 8/10 (+740ns): scatter:
                Task#6: pool=5
                Task#6 step 1/2 (+0s): 0s self time
                Task#6 step 2/2 (+0s): return nil
                Task#6 ends at 10.75µs
                  Gather#6: index=2
                  Gather#6 step 1/4 (+0s): 12.043µs self time
                  Gather#6 step 2/4 (+12.043µs): subjob:
                    Plan#2: pathCount=13 taskCount=30 maxPathDuration=8.438007ms minGatherCount=23 maxGatherCount=37
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
                      Task#68: pool=0
                      Task#68 step 1/2 (+0s): 10.064µs self time
                      Task#68 step 2/2 (+10.064µs): return nil
                      Task#68 ends at 10.064µs
                        Combine#68: index=0 flush=<nil>
                        Combine#68 step 1/4 (+0s): 174.437µs self time
                        Combine#68 step 2/4 (+174.437µs): scatter:
                          Task#66: pool=0
                          Task#66 step 1/2 (+0s): 21.798µs self time
                          Task#66 step 2/2 (+21.798µs): return nil
                          Task#66 ends at 206.299µs
                            Combine#66: index=0 flush=<nil>
                            Combine#66 step 1/4 (+0s): 6.118µs self time
                            Combine#66 step 2/4 (+6.118µs): scatter:
                              Task#13: pool=0
                              Task#13 step 1/2 (+0s): 32.201µs self time
                              Task#13 step 2/2 (+32.201µs): return nil
                              Task#13 ends at 244.618µs
                                Gather#13: index=13
                                Gather#13 step 1/2 (+0s): 999ns self time
                                Gather#13 step 2/2 (+999ns): return nil
                                Gather#13 ends at 245.617µs
                            Combine#66 step 3/4 (+6.118µs): 6.118µs self time
                            Combine#66 step 4/4 (+12.236µs): return nil
                            Combine#66 ends at 218.535µs
                        Combine#68 step 3/4 (+174.437µs): 174.479µs self time
                        Combine#68 step 4/4 (+348.916µs): return nil
                        Combine#68 ends at 358.98µs
                    Plan#2 step 2/3 (+0s): scatter:
                      Task#67: pool=0
                      Task#67 step 1/2 (+0s): 9.999µs self time
                      Task#67 step 2/2 (+9.999µs): return nil
                      Task#67 ends at 9.999µs
                        Combine#67: index=0 flush=<nil>
                        Combine#67 step 1/4 (+0s): 499.918µs self time
                        Combine#67 step 2/4 (+499.918µs): scatter:
                          Task#65: pool=0
                          Task#65 step 1/2 (+0s): 9.999µs self time
                          Task#65 step 2/2 (+9.999µs): return nil
                          Task#65 ends at 519.916µs
                            Combine#65: index=0 flush=<nil>
                            Combine#65 step 1/16 (+0s): 124ns self time
                            Combine#65 step 2/16 (+124ns): scatter:
                              Task#14: pool=0
                              Task#14 step 1/2 (+0s): 12.685µs self time
                              Task#14 step 2/2 (+12.685µs): return nil
                              Task#14 ends at 532.725µs
                                Gather#14: index=0
                                Gather#14 step 1/2 (+0s): 1µs self time
                                Gather#14 step 2/2 (+1µs): return nil
                                Gather#14 ends at 533.725µs
                            Combine#65 step 3/16 (+124ns): 119ns self time
                            Combine#65 step 4/16 (+243ns): scatter:
                              Task#62: pool=0
                              Task#62 step 1/2 (+0s): 9.993µs self time
                              Task#62 step 2/2 (+9.993µs): return nil
                              Task#62 ends at 530.152µs
                                Gather#62: index=12
                                Gather#62 step 1/4 (+0s): 412ns self time
                                Gather#62 step 2/4 (+412ns): scatter:
                                  Task#8: pool=0
                                  Task#8 step 1/2 (+0s): 9.998µs self time
                                  Task#8 step 2/2 (+9.998µs): return nil
                                  Task#8 ends at 540.562µs
                                    Gather#8: index=10
                                    Gather#8 step 1/2 (+0s): 851ns self time
                                    Gather#8 step 2/2 (+851ns): return nil
                                    Gather#8 ends at 541.413µs
                                Gather#62 step 3/4 (+412ns): 520ns self time
                                Gather#62 step 4/4 (+932ns): return nil
                                Gather#62 ends at 531.084µs
                            Combine#65 step 5/16 (+243ns): 111ns self time
                            Combine#65 step 6/16 (+354ns): scatter:
                              Task#18: pool=0
                              Task#18 step 1/2 (+0s): 9.999µs self time
                              Task#18 step 2/2 (+9.999µs): return nil
                              Task#18 ends at 530.269µs
                                Gather#18: index=3
                                Gather#18 step 1/2 (+0s): 1.505µs self time
                                Gather#18 step 2/2 (+1.505µs): return nil
                                Gather#18 ends at 531.774µs
                            Combine#65 step 7/16 (+354ns): 128ns self time
                            Combine#65 step 8/16 (+482ns): scatter:
                              Task#63: pool=0
                              Task#63 step 1/2 (+0s): 817.951µs self time
                              Task#63 step 2/2 (+817.951µs): return nil
                              Task#63 ends at 1.338349ms
                                Gather#63: index=8
                                Gather#63 step 1/8 (+0s): 238ns self time
                                Gather#63 step 2/8 (+238ns): scatter:
                                  Task#56: pool=0
                                  Task#56 step 1/2 (+0s): 9.932µs self time
                                  Task#56 step 2/2 (+9.932µs): return nil
                                  Task#56 ends at 1.348519ms
                                    Gather#56: index=2
                                    Gather#56 step 1/4 (+0s): 507ns self time
                                    Gather#56 step 2/4 (+507ns): scatter:
                                      Task#22: pool=0
                                      Task#22 step 1/4 (+0s): 5.01µs self time
                                      Task#22 step 2/4 (+5.01µs): subjob:
                                        Plan#3: pathCount=15 taskCount=32 maxPathDuration=4.335706ms minGatherCount=26 maxGatherCount=56
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
                                        Plan#3 step 1/6 (+0s): scatter:
                                          Task#50: pool=0
                                          Task#50 step 1/2 (+0s): 9.999µs self time
                                          Task#50 step 2/2 (+9.999µs): return nil
                                          Task#50 ends at 9.999µs
                                            Combine#50: index=3 flush=<nil>
                                            Combine#50 step 1/4 (+0s): 498ns self time
                                            Combine#50 step 2/4 (+498ns): scatter:
                                              Task#34: pool=0
                                              Task#34 step 1/2 (+0s): 8.177µs self time
                                              Task#34 step 2/2 (+8.177µs): return nil
                                              Task#34 ends at 18.674µs
                                                Gather#34: index=1
                                                Gather#34 step 1/2 (+0s): 985ns self time
                                                Gather#34 step 2/2 (+985ns): return nil
                                                Gather#34 ends at 19.659µs
                                            Combine#50 step 3/4 (+498ns): 501ns self time
                                            Combine#50 step 4/4 (+999ns): return nil
                                            Combine#50 ends at 10.998µs
                                        Plan#3 step 2/6 (+0s): scatter:
                                          Task#51: pool=3
                                          Task#51 step 1/2 (+0s): 9.862µs self time
                                          Task#51 step 2/2 (+9.862µs): return nil
                                          Task#51 ends at 9.862µs
                                            Gather#51: index=3
                                            Gather#51 step 1/4 (+0s): 8.105µs self time
                                            Gather#51 step 2/4 (+8.105µs): scatter:
                                              Task#49: pool=0
                                              Task#49 step 1/2 (+0s): 2.157µs self time
                                              Task#49 step 2/2 (+2.157µs): return nil
                                              Task#49 ends at 20.124µs
                                                Gather#49: index=1
                                                Gather#49 step 1/8 (+0s): 459ns self time
                                                Gather#49 step 2/8 (+459ns): scatter:
                                                  Task#26: pool=6
                                                  Task#26 step 1/2 (+0s): 6.025µs self time
                                                  Task#26 step 2/2 (+6.025µs): return nil
                                                  Task#26 ends at 26.608µs
                                                    Gather#26: index=3
                                                    Gather#26 step 1/2 (+0s): 1.001µs self time
                                                    Gather#26 step 2/2 (+1.001µs): return nil
                                                    Gather#26 ends at 27.609µs
                                                Gather#49 step 3/8 (+459ns): 260ns self time
                                                Gather#49 step 4/8 (+719ns): scatter:
                                                  Task#43: pool=0
                                                  Task#43 step 1/2 (+0s): 4.270827ms self time
                                                  Task#43 step 2/2 (+4.270827ms): return nil
                                                  Task#43 ends at 4.29167ms
                                                    Gather#43: index=0
                                                    Gather#43 step 1/4 (+0s): 656ns self time
                                                    Gather#43 step 2/4 (+656ns): scatter:
                                                      Task#40: pool=0
                                                      Task#40 step 1/2 (+0s): 31.14µs self time
                                                      Task#40 step 2/2 (+31.14µs): return error
                                                      Task#40 ends at 4.323466ms
                                                        Combine#40: index=3 flush=<nil>
                                                        Combine#40 step 1/4 (+0s): 1.288µs self time
                                                        Combine#40 step 2/4 (+1.288µs): scatter:
                                                          Task#37: pool=7
                                                          Task#37 step 1/2 (+0s): 9.955µs self time
                                                          Task#37 step 2/2 (+9.955µs): return nil
                                                          Task#37 ends at 4.334709ms
                                                            Combine#37: index=3 flush=<nil>
                                                            Combine#37 step 1/2 (+0s): 997ns self time
                                                            Combine#37 step 2/2 (+997ns): return nil
                                                            Combine#37 ends at 4.335706ms
                                                        Combine#40 step 3/4 (+1.288µs): 1.29µs self time
                                                        Combine#40 step 4/4 (+2.578µs): return nil
                                                        Combine#40 ends at 4.326044ms
                                                    Gather#43 step 3/4 (+656ns): 86ns self time
                                                    Gather#43 step 4/4 (+742ns): return nil
                                                    Gather#43 ends at 4.292412ms
                                                Gather#49 step 5/8 (+719ns): 211ns self time
                                                Gather#49 step 6/8 (+930ns): scatter:
                                                  Task#31: pool=7
                                                  Task#31 step 1/2 (+0s): 10.489µs self time
                                                  Task#31 step 2/2 (+10.489µs): return nil
                                                  Task#31 ends at 31.543µs
                                                    Gather#31: index=1
                                                    Gather#31 step 1/2 (+0s): 999ns self time
                                                    Gather#31 step 2/2 (+999ns): return nil
                                                    Gather#31 ends at 32.542µs
                                                Gather#49 step 7/8 (+930ns): 926ns self time
                                                Gather#49 step 8/8 (+1.856µs): return nil
                                                Gather#49 ends at 21.98µs
                                            Gather#51 step 3/4 (+8.105µs): 8.115µs self time
                                            Gather#51 step 4/4 (+16.22µs): return nil
                                            Gather#51 ends at 26.082µs
                                        Plan#3 step 3/6 (+0s): scatter:
                                          Task#54: pool=4
                                          Task#54 step 1/2 (+0s): 10.323µs self time
                                          Task#54 step 2/2 (+10.323µs): return nil
                                          Task#54 ends at 10.323µs
                                            Combine#54: index=2 flush=<nil>
                                            Combine#54 step 1/4 (+0s): 547ns self time
                                            Combine#54 step 2/4 (+547ns): scatter:
                                              Task#23: pool=7
                                              Task#23 step 1/2 (+0s): 9.999µs self time
                                              Task#23 step 2/2 (+9.999µs): return nil
                                              Task#23 ends at 20.869µs
                                                Gather#23: index=2
                                                Gather#23 step 1/2 (+0s): 998ns self time
                                                Gather#23 step 2/2 (+998ns): return nil
                                                Gather#23 ends at 21.867µs
                                            Combine#54 step 3/4 (+547ns): 463ns self time
                                            Combine#54 step 4/4 (+1.01µs): return nil
                                            Combine#54 ends at 11.333µs
                                        Plan#3 step 4/6 (+0s): scatter:
                                          Task#52: pool=1
                                          Task#52 step 1/2 (+0s): 9.998µs self time
                                          Task#52 step 2/2 (+9.998µs): return nil
                                          Task#52 ends at 9.998µs
                                            Gather#52: index=1
                                            Gather#52 step 1/10 (+0s): 1.041µs self time
                                            Gather#52 step 2/10 (+1.041µs): scatter:
                                              Task#46: pool=0
                                              Task#46 step 1/2 (+0s): 9.998µs self time
                                              Task#46 step 2/2 (+9.998µs): return nil
                                              Task#46 ends at 21.037µs
                                                Gather#46: index=3
                                                Gather#46 step 1/4 (+0s): 548ns self time
                                                Gather#46 step 2/4 (+548ns): scatter:
                                                  Task#44: pool=6
                                                  Task#44 step 1/2 (+0s): 9.985µs self time
                                                  Task#44 step 2/2 (+9.985µs): return nil
                                                  Task#44 ends at 31.57µs
                                                    Gather#44: index=3
                                                    Gather#44 step 1/6 (+0s): 334ns self time
                                                    Gather#44 step 2/6 (+334ns): scatter:
                                                      Task#41: pool=2
                                                      Task#41 step 1/2 (+0s): 10.001µs self time
                                                      Task#41 step 2/2 (+10.001µs): return nil
                                                      Task#41 ends at 41.905µs
                                                        Gather#41: index=2
                                                        Gather#41 step 1/4 (+0s): 502ns self time
                                                        Gather#41 step 2/4 (+502ns): scatter:
                                                          Task#29: pool=6
                                                          Task#29 step 1/2 (+0s): 11.061µs self time
                                                          Task#29 step 2/2 (+11.061µs): return nil
                                                          Task#29 ends at 53.468µs
                                                            Gather#29: index=3
                                                            Gather#29 step 1/2 (+0s): 984ns self time
                                                            Gather#29 step 2/2 (+984ns): return nil
                                                            Gather#29 ends at 54.452µs
                                                        Gather#41 step 3/4 (+502ns): 503ns self time
                                                        Gather#41 step 4/4 (+1.005µs): return nil
                                                        Gather#41 ends at 42.91µs
                                                    Gather#44 step 3/6 (+334ns): 44ns self time
                                                    Gather#44 step 4/6 (+378ns): scatter:
                                                      Task#39: pool=2
                                                      Task#39 step 1/2 (+0s): 9.993µs self time
                                                      Task#39 step 2/2 (+9.993µs): return nil
                                                      Task#39 ends at 41.941µs
                                                        Gather#39: index=3
                                                        Gather#39 step 1/4 (+0s): 651ns self time
                                                        Gather#39 step 2/4 (+651ns): scatter:
                                                          Task#38: pool=3
                                                          Task#38 step 1/2 (+0s): 9.763µs self time
                                                          Task#38 step 2/2 (+9.763µs): return nil
                                                          Task#38 ends at 52.355µs
                                                            Combine#38: index=0 flush=Gather#38
                                                            Combine#38 step 1/2 (+0s): 1.04µs self time
                                                            Combine#38 step 2/2 (+1.04µs): return nil
                                                            Combine#38 ends at 53.395µs
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
                                                        Gather#39 ends at 43.241µs
                                                    Gather#44 step 5/6 (+378ns): 631ns self time
                                                    Gather#44 step 6/6 (+1.009µs): return nil
                                                    Gather#44 ends at 32.579µs
                                                Gather#46 step 3/4 (+548ns): 609ns self time
                                                Gather#46 step 4/4 (+1.157µs): return nil
                                                Gather#46 ends at 22.194µs
                                            Gather#52 step 3/10 (+1.041µs): 0s self time
                                            Gather#52 step 4/10 (+1.041µs): scatter:
                                              Task#48: pool=0
                                              Task#48 step 1/2 (+0s): 9.991µs self time
                                              Task#48 step 2/2 (+9.991µs): return nil
                                              Task#48 ends at 21.03µs
                                                Gather#48: index=2
                                                Gather#48 step 1/4 (+0s): 309.91µs self time
                                                Gather#48 step 2/4 (+309.91µs): scatter:
                                                  Task#45: pool=7
                                                  Task#45 step 1/2 (+0s): 364ns self time
                                                  Task#45 step 2/2 (+364ns): return nil
                                                  Task#45 ends at 331.304µs
                                                    Gather#45: index=3
                                                    Gather#45 step 1/4 (+0s): 121.594µs self time
                                                    Gather#45 step 2/4 (+121.594µs): scatter:
                                                      Task#30: pool=0
                                                      Task#30 step 1/2 (+0s): 10µs self time
                                                      Task#30 step 2/2 (+10µs): return nil
                                                      Task#30 ends at 462.898µs
                                                        Combine#30: index=3 flush=<nil>
                                                        Combine#30 step 1/2 (+0s): 1.472µs self time
                                                        Combine#30 step 2/2 (+1.472µs): return nil
                                                        Combine#30 ends at 464.37µs
                                                    Gather#45 step 3/4 (+121.594µs): 368.673µs self time
                                                    Gather#45 step 4/4 (+490.267µs): return nil
                                                    Gather#45 ends at 821.571µs
                                                Gather#48 step 3/4 (+309.91µs): 309.904µs self time
                                                Gather#48 step 4/4 (+619.814µs): return nil
                                                Gather#48 ends at 640.844µs
                                            Gather#52 step 5/10 (+1.041µs): 0s self time
                                            Gather#52 step 6/10 (+1.041µs): scatter:
                                              Task#35: pool=0
                                              Task#35 step 1/2 (+0s): 9.994µs self time
                                              Task#35 step 2/2 (+9.994µs): return nil
                                              Task#35 ends at 21.033µs
                                                Gather#35: index=0
                                                Gather#35 step 1/2 (+0s): 651.102µs self time
                                                Gather#35 step 2/2 (+651.102µs): return nil
                                                Gather#35 ends at 672.135µs
                                            Gather#52 step 7/10 (+1.041µs): 0s self time
                                            Gather#52 step 8/10 (+1.041µs): scatter:
                                              Task#47: pool=7
                                              Task#47 step 1/2 (+0s): 9.976µs self time
                                              Task#47 step 2/2 (+9.976µs): return nil
                                              Task#47 ends at 21.015µs
                                                Gather#47: index=1
                                                Gather#47 step 1/6 (+0s): 34ns self time
                                                Gather#47 step 2/6 (+34ns): scatter:
                                                  Task#24: pool=2
                                                  Task#24 step 1/2 (+0s): 9.947µs self time
                                                  Task#24 step 2/2 (+9.947µs): return nil
                                                  Task#24 ends at 30.996µs
                                                    Gather#24: index=3
                                                    Gather#24 step 1/2 (+0s): 1.961µs self time
                                                    Gather#24 step 2/2 (+1.961µs): return nil
                                                    Gather#24 ends at 32.957µs
                                                Gather#47 step 3/6 (+34ns): 797ns self time
                                                Gather#47 step 4/6 (+831ns): scatter:
                                                  Task#42: pool=1
                                                  Task#42 step 1/2 (+0s): 9.999µs self time
                                                  Task#42 step 2/2 (+9.999µs): return nil
                                                  Task#42 ends at 31.845µs
                                                    Gather#42: index=0
                                                    Gather#42 step 1/4 (+0s): 750ns self time
                                                    Gather#42 step 2/4 (+750ns): scatter:
                                                      Task#27: pool=2
                                                      Task#27 step 1/2 (+0s): 10.392µs self time
                                                      Task#27 step 2/2 (+10.392µs): return nil
                                                      Task#27 ends at 42.987µs
                                                        Gather#27: index=3
                                                        Gather#27 step 1/2 (+0s): 458.476µs self time
                                                        Gather#27 step 2/2 (+458.476µs): return nil
                                                        Gather#27 ends at 501.463µs
                                                    Gather#42 step 3/4 (+750ns): 248ns self time
                                                    Gather#42 step 4/4 (+998ns): return nil
                                                    Gather#42 ends at 32.843µs
                                                Gather#47 step 5/6 (+831ns): 159ns self time
                                                Gather#47 step 6/6 (+990ns): return nil
                                                Gather#47 ends at 22.005µs
                                            Gather#52 step 9/10 (+1.041µs): 0s self time
                                            Gather#52 step 10/10 (+1.041µs): return nil
                                            Gather#52 ends at 11.039µs
                                        Plan#3 step 5/6 (+0s): scatter:
                                          Task#53: pool=3
                                          Task#53 step 1/2 (+0s): 11.111µs self time
                                          Task#53 step 2/2 (+11.111µs): return nil
                                          Task#53 ends at 11.111µs
                                            Combine#53: index=0 flush=<nil>
                                            Combine#53 step 1/8 (+0s): 46ns self time
                                            Combine#53 step 2/8 (+46ns): scatter:
                                              Task#28: pool=3
                                              Task#28 step 1/2 (+0s): 10.001µs self time
                                              Task#28 step 2/2 (+10.001µs): return error
                                              Task#28 ends at 21.158µs
                                                Gather#28: index=0
                                                Gather#28 step 1/2 (+0s): 88.454µs self time
                                                Gather#28 step 2/2 (+88.454µs): return nil
                                                Gather#28 ends at 109.612µs
                                            Combine#53 step 3/8 (+46ns): 38ns self time
                                            Combine#53 step 4/8 (+84ns): scatter:
                                              Task#36: pool=3
                                              Task#36 step 1/2 (+0s): 69.594µs self time
                                              Task#36 step 2/2 (+69.594µs): return nil
                                              Task#36 ends at 80.789µs
                                                Gather#36: index=1
                                                Gather#36 step 1/2 (+0s): 1.007µs self time
                                                Gather#36 step 2/2 (+1.007µs): return nil
                                                Gather#36 ends at 81.796µs
                                            Combine#53 step 5/8 (+84ns): 55ns self time
                                            Combine#53 step 6/8 (+139ns): scatter:
                                              Task#25: pool=0
                                              Task#25 step 1/2 (+0s): 75.042µs self time
                                              Task#25 step 2/2 (+75.042µs): return nil
                                              Task#25 ends at 86.292µs
                                                Gather#25: index=2
                                                Gather#25 step 1/2 (+0s): 1.001µs self time
                                                Gather#25 step 2/2 (+1.001µs): return nil
                                                Gather#25 ends at 87.293µs
                                            Combine#53 step 7/8 (+139ns): 23ns self time
                                            Combine#53 step 8/8 (+162ns): return nil
                                            Combine#53 ends at 11.273µs
                                        Plan#3 step 6/6 (+0s): ends at 4.335706ms
                                      Task#22 step 3/4 (+4.340716ms): 5.009µs self time
                                      Task#22 step 4/4 (+4.345725ms): return nil
                                      Task#22 ends at 5.694751ms
                                        Gather#22: index=4
                                        Gather#22 step 1/4 (+0s): 55ns self time
                                        Gather#22 step 2/4 (+55ns): scatter:
                                          Task#12: pool=0
                                          Task#12 step 1/2 (+0s): 9.889µs self time
                                          Task#12 step 2/2 (+9.889µs): return nil
                                          Task#12 ends at 5.704695ms
                                            Gather#12: index=0
                                            Gather#12 step 1/2 (+0s): 4.265µs self time
                                            Gather#12 step 2/2 (+4.265µs): return nil
                                            Gather#12 ends at 5.70896ms
                                        Gather#22 step 3/4 (+55ns): 943ns self time
                                        Gather#22 step 4/4 (+998ns): return nil
                                        Gather#22 ends at 5.695749ms
                                    Gather#56 step 3/4 (+507ns): 510ns self time
                                    Gather#56 step 4/4 (+1.017µs): return nil
                                    Gather#56 ends at 1.349536ms
                                Gather#63 step 3/8 (+238ns): 62ns self time
                                Gather#63 step 4/8 (+300ns): scatter:
                                  Task#59: pool=0
                                  Task#59 step 1/2 (+0s): 9.999µs self time
                                  Task#59 step 2/2 (+9.999µs): return nil
                                  Task#59 ends at 1.348648ms
                                    Combine#59: index=0 flush=<nil>
                                    Combine#59 step 1/6 (+0s): 9.857µs self time
                                    Combine#59 step 2/6 (+9.857µs): scatter:
                                      Task#11: pool=0
                                      Task#11 step 1/2 (+0s): 9.998µs self time
                                      Task#11 step 2/2 (+9.998µs): return nil
                                      Task#11 ends at 1.368503ms
                                        Gather#11: index=3
                                        Gather#11 step 1/2 (+0s): 1µs self time
                                        Gather#11 step 2/2 (+1µs): return nil
                                        Gather#11 ends at 1.369503ms
                                    Combine#59 step 3/6 (+9.857µs): 10.088µs self time
                                    Combine#59 step 4/6 (+19.945µs): scatter:
                                      Task#9: pool=0
                                      Task#9 step 1/2 (+0s): 10.022µs self time
                                      Task#9 step 2/2 (+10.022µs): return nil
                                      Task#9 ends at 1.378615ms
                                        Gather#9: index=2
                                        Gather#9 step 1/2 (+0s): 512ns self time
                                        Gather#9 step 2/2 (+512ns): return nil
                                        Gather#9 ends at 1.379127ms
                                    Combine#59 step 5/6 (+19.945µs): 9.642µs self time
                                    Combine#59 step 6/6 (+29.587µs): return nil
                                    Combine#59 ends at 1.378235ms
                                Gather#63 step 5/8 (+300ns): 350ns self time
                                Gather#63 step 6/8 (+650ns): scatter:
                                  Task#17: pool=0
                                  Task#17 step 1/2 (+0s): 1.843416ms self time
                                  Task#17 step 2/2 (+1.843416ms): return nil
                                  Task#17 ends at 3.182415ms
                                    Combine#17: index=0 flush=<nil>
                                    Combine#17 step 1/2 (+0s): 886ns self time
                                    Combine#17 step 2/2 (+886ns): return nil
                                    Combine#17 ends at 3.183301ms
                                Gather#63 step 7/8 (+650ns): 348ns self time
                                Gather#63 step 8/8 (+998ns): return nil
                                Gather#63 ends at 1.339347ms
                            Combine#65 step 9/16 (+482ns): 128ns self time
                            Combine#65 step 10/16 (+610ns): scatter:
                              Task#16: pool=0
                              Task#16 step 1/2 (+0s): 0s self time
                              Task#16 step 2/2 (+0s): return nil
                              Task#16 ends at 520.526µs
                                Gather#16: index=1
                                Gather#16 step 1/2 (+0s): 1.002µs self time
                                Gather#16 step 2/2 (+1.002µs): return nil
                                Gather#16 ends at 521.528µs
                            Combine#65 step 11/16 (+610ns): 76ns self time
                            Combine#65 step 12/16 (+686ns): scatter:
                              Task#64: pool=0
                              Task#64 step 1/2 (+0s): 4.583µs self time
                              Task#64 step 2/2 (+4.583µs): return nil
                              Task#64 ends at 525.185µs
                                Gather#64: index=10
                                Gather#64 step 1/4 (+0s): 389ns self time
                                Gather#64 step 2/4 (+389ns): scatter:
                                  Task#57: pool=0
                                  Task#57 step 1/2 (+0s): 5.536986ms self time
                                  Task#57 step 2/2 (+5.536986ms): return nil
                                  Task#57 ends at 6.06256ms
                                    Gather#57: index=15
                                    Gather#57 step 1/4 (+0s): 526ns self time
                                    Gather#57 step 2/4 (+526ns): scatter:
                                      Task#21: pool=0
                                      Task#21 step 1/2 (+0s): 9.996µs self time
                                      Task#21 step 2/2 (+9.996µs): return nil
                                      Task#21 ends at 6.073082ms
                                        Gather#21: index=14
                                        Gather#21 step 1/4 (+0s): 414.18µs self time
                                        Gather#21 step 2/4 (+414.18µs): scatter:
                                          Task#15: pool=0
                                          Task#15 step 1/2 (+0s): 71.13µs self time
                                          Task#15 step 2/2 (+71.13µs): return nil
                                          Task#15 ends at 6.558392ms
                                            Gather#15: index=0
                                            Gather#15 step 1/2 (+0s): 1.039µs self time
                                            Gather#15 step 2/2 (+1.039µs): return nil
                                            Gather#15 ends at 6.559431ms
                                        Gather#21 step 3/4 (+414.18µs): 414.191µs self time
                                        Gather#21 step 4/4 (+828.371µs): return nil
                                        Gather#21 ends at 6.901453ms
                                    Gather#57 step 3/4 (+526ns): 523ns self time
                                    Gather#57 step 4/4 (+1.049µs): return nil
                                    Gather#57 ends at 6.063609ms
                                Gather#64 step 3/4 (+389ns): 4.639µs self time
                                Gather#64 step 4/4 (+5.028µs): return nil
                                Gather#64 ends at 530.213µs
                            Combine#65 step 13/16 (+686ns): 60ns self time
                            Combine#65 step 14/16 (+746ns): scatter:
                              Task#61: pool=0
                              Task#61 step 1/2 (+0s): 6.903704ms self time
                              Task#61 step 2/2 (+6.903704ms): return nil
                              Task#61 ends at 7.424366ms
                                Gather#61: index=11
                                Gather#61 step 1/6 (+0s): 294ns self time
                                Gather#61 step 2/6 (+294ns): scatter:
                                  Task#58: pool=0
                                  Task#58 step 1/2 (+0s): 10.012µs self time
                                  Task#58 step 2/2 (+10.012µs): return nil
                                  Task#58 ends at 7.434672ms
                                    Combine#58: index=0 flush=<nil>
                                    Combine#58 step 1/4 (+0s): 433ns self time
                                    Combine#58 step 2/4 (+433ns): scatter:
                                      Task#55: pool=0
                                      Task#55 step 1/2 (+0s): 10.57µs self time
                                      Task#55 step 2/2 (+10.57µs): return nil
                                      Task#55 ends at 7.445675ms
                                        Gather#55: index=5
                                        Gather#55 step 1/4 (+0s): 910ns self time
                                        Gather#55 step 2/4 (+910ns): scatter:
                                          Task#19: pool=0
                                          Task#19 step 1/2 (+0s): 9.996µs self time
                                          Task#19 step 2/2 (+9.996µs): return nil
                                          Task#19 ends at 7.456581ms
                                            Gather#19: index=4
                                            Gather#19 step 1/2 (+0s): 3.095µs self time
                                            Gather#19 step 2/2 (+3.095µs): return nil
                                            Gather#19 ends at 7.459676ms
                                        Gather#55 step 3/4 (+910ns): 93ns self time
                                        Gather#55 step 4/4 (+1.003µs): return nil
                                        Gather#55 ends at 7.446678ms
                                    Combine#58 step 3/4 (+433ns): 385ns self time
                                    Combine#58 step 4/4 (+818ns): return nil
                                    Combine#58 ends at 7.43549ms
                                Gather#61 step 3/6 (+294ns): 186ns self time
                                Gather#61 step 4/6 (+480ns): scatter:
                                  Task#60: pool=0
                                  Task#60 step 1/2 (+0s): 9.999µs self time
                                  Task#60 step 2/2 (+9.999µs): return nil
                                  Task#60 ends at 7.434845ms
                                    Gather#60: index=10
                                    Gather#60 step 1/6 (+0s): 518ns self time
                                    Gather#60 step 2/6 (+518ns): scatter:
                                      Task#20: pool=0
                                      Task#20 step 1/2 (+0s): 10.002µs self time
                                      Task#20 step 2/2 (+10.002µs): return nil
                                      Task#20 ends at 7.445365ms
                                        Gather#20: index=7
                                        Gather#20 step 1/4 (+0s): 513ns self time
                                        Gather#20 step 2/4 (+513ns): scatter:
                                          Task#7: pool=0
                                          Task#7 step 1/2 (+0s): 10.008µs self time
                                          Task#7 step 2/2 (+10.008µs): return nil
                                          Task#7 ends at 7.455886ms
                                            Gather#7: index=3
                                            Gather#7 step 1/2 (+0s): 601ns self time
                                            Gather#7 step 2/2 (+601ns): return error
                                            Gather#7 ends at 7.456487ms
                                        Gather#20 step 3/4 (+513ns): 487ns self time
                                        Gather#20 step 4/4 (+1µs): return nil
                                        Gather#20 ends at 7.446365ms
                                    Gather#60 step 3/6 (+518ns): 436ns self time
                                    Gather#60 step 4/6 (+954ns): scatter:
                                      Task#10: pool=0
                                      Task#10 step 1/2 (+0s): 2.208µs self time
                                      Task#10 step 2/2 (+2.208µs): return nil
                                      Task#10 ends at 7.438007ms
                                        Gather#10: index=4
                                        Gather#10 step 1/2 (+0s): 1ms self time
                                        Gather#10 step 2/2 (+1ms): return nil
                                        Gather#10 ends at 8.438007ms
                                    Gather#60 step 5/6 (+954ns): 652ns self time
                                    Gather#60 step 6/6 (+1.606µs): return nil
                                    Gather#60 ends at 7.436451ms
                                Gather#61 step 5/6 (+480ns): 426ns self time
                                Gather#61 step 6/6 (+906ns): return error
                                Gather#61 ends at 7.425272ms
                            Combine#65 step 15/16 (+746ns): 256ns self time
                            Combine#65 step 16/16 (+1.002µs): return nil
                            Combine#65 ends at 520.918µs
                        Combine#67 step 3/4 (+499.918µs): 499.905µs self time
                        Combine#67 step 4/4 (+999.823µs): return nil
                        Combine#67 ends at 1.009822ms
                    Plan#2 step 3/3 (+0s): ends at 8.438007ms
                  Gather#6 step 3/4 (+8.45005ms): 11.956µs self time
                  Gather#6 step 4/4 (+8.462006ms): return nil
                  Gather#6 ends at 8.472756ms
              Gather#331 step 9/10 (+740ns): 274ns self time
              Gather#331 step 10/10 (+1.014µs): return nil
              Gather#331 ends at 11.024µs
          Plan#1 step 3/3 (+0s): ends at 26.781267ms
        Gather#0 step 3/4 (+26.782146ms): 128ns self time
        Gather#0 step 4/4 (+26.782274ms): return nil
        Gather#0 ends at 29.100037ms
    Gather#367 step 7/8 (+169ns): 98ns self time
    Gather#367 step 8/8 (+267ns): return nil
    Gather#367 ends at 2.30786ms
Plan#0 step 3/4 (+0s): scatter:
  Task#368: pool=1
  Task#368 step 1/2 (+0s): 36.072µs self time
  Task#368 step 2/2 (+36.072µs): return nil
  Task#368 ends at 36.072µs
    Combine#368: index=3 flush=<nil>
    Combine#368 step 1/4 (+0s): 496ns self time
    Combine#368 step 2/4 (+496ns): scatter:
      Task#360: pool=0
      Task#360 step 1/2 (+0s): 7.352872ms self time
      Task#360 step 2/2 (+7.352872ms): return nil
      Task#360 ends at 7.38944ms
        Gather#360: index=0
        Gather#360 step 1/2 (+0s): 612ns self time
        Gather#360 step 2/2 (+612ns): return nil
        Gather#360 ends at 7.390052ms
    Combine#368 step 3/4 (+496ns): 504ns self time
    Combine#368 step 4/4 (+1µs): return nil
    Combine#368 ends at 37.072µs
Plan#0 step 4/4 (+0s): ends at 29.100037ms`

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
