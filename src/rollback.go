package main

import (
	"fmt"
	"math"
	"time"

	ggpo "github.com/assemblaj/ggpo"
	lua "github.com/yuin/gopher-lua"
)

type RollbackSystem struct {
	session       *RollbackSession
	currentFight  Fight
	netConnection *NetConnection
	ggpoInputs    []InputBits
}

type RollbackProperties struct {
	FrameDelay            int  `ini:"FrameDelay"`
	DisconnectNotifyStart int  `ini:"DisconnectNotifyStart"`
	DisconnectTimeout     int  `ini:"DisconnectTimeout"`
	LogsEnabled           bool `ini:"LogsEnabled"`
	SaveStageData         bool `ini:"SaveStageData"`
	DesyncTest            bool `ini:"DesyncTest"`
	DesyncTestFrames      int  `ini:"DesyncTestFrames"`
	DesyncTestAI          bool `ini:"DesyncTestAI"`
}

// Currently system.go fight() is just short circuited to use this one instead during rollback
// TODO: Merge with system.go
func (rs *RollbackSystem) fight(s *System) bool {
	// Reset variables
	s.gameTime, s.paused, s.accel = 0, false, 1
	s.aiInput = [len(s.aiInput)]AiInput{}
	rs.currentFight = NewFight()
	rs.ggpoInputs = make([]InputBits, 2)

	// Defer resetting variables on return
	defer rs.currentFight.endFight()

	s.debugWC = sys.chars[0][0]
	// debugInput := func() {
	// 	select {
	// 	case cl := <-s.commandLine:
	// 		if err := s.luaLState.DoString(cl); err != nil {
	// 			s.errLog.Println(err.Error())
	// 		}
	// 	default:
	// 	}
	// }

	// Synchronize with external inputs (netplay, replays, etc)
	if err := s.synchronize(); err != nil {
		s.errLog.Println(err.Error())
		s.esc = true
	}
	if s.netConnection != nil {
		defer s.netConnection.Stop()
	}
	s.wincnt.init()

	rs.currentFight.initSuperMeter()
	rs.currentFight.initTeamsLevels()

	rs.currentFight.initChars()

	var didTryLoadBGM bool

	// default bgm playback, used only in Quick VS or if externalized Lua implementaion is disabled
	if s.round == 1 && (s.gameMode == "" || len(sys.cfg.Common.Lua) == 0) && sys.stage.stageTime > 0 && !didTryLoadBGM {
		// Need to search first
		LoadFile(&s.stage.bgmusic, []string{s.stage.def, "", "sound/"}, func(path string) error {
			s.bgm.Open(path, 1, int(s.stage.bgmvolume), int(s.stage.bgmloopstart), int(s.stage.bgmloopend), int(s.stage.bgmstartposition), s.stage.bgmfreqmul, -1)
			didTryLoadBGM = true
			return nil
		})
	}

	rs.currentFight.oldWins, rs.currentFight.oldDraws = s.wins, s.draws
	rs.currentFight.oldTeamLeader = s.teamLeader

	var running bool
	if rs.session != nil && s.netConnection != nil {
		if rs.session.host != "" {
			rs.session.InitP2(2, 7550, 7600, rs.session.host)
			rs.session.playerNo = 2
		} else {
			rs.session.InitP1(2, 7600, 7550, rs.session.remoteIp)
			rs.session.playerNo = 1
		}
		s.time = rs.session.netTime
		s.preFightTime = s.netConnection.preFightTime
		//if !rs.session.IsConnected() {
		// for !rs.session.synchronized {
		// 	rs.session.backend.Idle(0)
		// }
		//}
		// s.netConnection.Close()
		rs.session.recording = s.netConnection.recording
		rs.netConnection = s.netConnection
		s.netConnection = nil
	} else if s.netConnection == nil && rs.session == nil {
		session := NewRollbackSession(s.cfg.Netplay.Rollback)
		rs.session = &session
		rs.session.InitSyncTest(2)
	}
	rs.session.netTime = 0
	rs.currentFight.reset()

	// These empty frames help the netcode stabilize. Without them, the chances of it desyncing at match start increase a lot
	for i := 0; i < 120; i++ {
		err := rs.session.backend.Idle(
			int(math.Max(0, float64(120))))
		fmt.Printf("difference: %d\n", rs.session.next-rs.session.now-1)
		if err != nil {
			panic(err)
		}

		s.renderFrame() // Do we need to render at this point? Is there anything to render?

		//rs.session.loopTimer.usToWaitThisLoop()
		running = s.update()

		if !running {
			break
		}
	}

	// Loop until end of match
	///fin := false
	for !s.endMatch {
		rs.session.now = time.Now().UnixMilli()
		err := rs.session.backend.Idle(
			int(math.Max(0, float64(rs.session.next-rs.session.now-1))))
		if err != nil {
			panic(err)
		}

		running = rs.runFrame(s)

		if rs.currentFight.fin && (!s.postMatchFlg || len(s.cfg.Common.Lua) == 0) {
			break
		}

		rs.session.next = rs.session.now + 1000/60

		if !running {
			break
		}

		s.renderFrame()

		//rs.session.loopTimer.usToWaitThisLoop()
		running = s.update()

		if !running {
			break
		}
	}
	rs.session.SaveReplay()
	// sys.esc = true
	//sys.rollback.currentFight.fin = true
	s.netConnection = rs.netConnection
	rs.session.backend.Close()

	// Prep for the next match.
	if s.netConnection != nil {
		newSession := NewRollbackSession(s.cfg.Netplay.Rollback)
		host := rs.session.host
		remoteIp := rs.session.remoteIp

		rs.session = &newSession
		rs.session.host = host
		rs.session.remoteIp = remoteIp
	} else {
		rs.session = nil
	}
	return false
}

// Called once per frame by the main game loop
// Responsible for collecting local inputs and driving the GGPO backend forward
func (rs *RollbackSystem) runFrame(s *System) bool {
	var buffer []byte
	var ggpoerr error

	if rs.session.syncTest && rs.session.netTime == 0 {
		if rs.session.config.DesyncTestAI {
			buffer = getAIInputs(0)
			ggpoerr = rs.session.backend.AddLocalInput(ggpo.PlayerHandle(0), buffer, len(buffer))
			buffer = getAIInputs(1)
			ggpoerr = rs.session.backend.AddLocalInput(ggpo.PlayerHandle(1), buffer, len(buffer))
		} else {
			buffer = rs.getInputs(0)
			ggpoerr = rs.session.backend.AddLocalInput(ggpo.PlayerHandle(0), buffer, len(buffer))
			buffer = rs.getInputs(1)
			ggpoerr = rs.session.backend.AddLocalInput(ggpo.PlayerHandle(1), buffer, len(buffer))
		}
	} else {
		buffer = rs.getInputs(0)
		ggpoerr = rs.session.backend.AddLocalInput(rs.session.currentPlayerHandle, buffer, len(buffer))
	}

	if ggpoerr == nil {
		// Get speculative inputs for the local player for this frame
		var values [][]byte
		disconnectFlags := 0
		values, ggpoerr = rs.session.backend.SyncInput(&disconnectFlags)
		rs.ggpoInputs = decodeInputs(values)

		if rs.session.recording != nil {
			rs.session.SetInput(rs.session.netTime, 0, rs.ggpoInputs[0])
			rs.session.SetInput(rs.session.netTime, 1, rs.ggpoInputs[1])
			rs.session.netTime++
		}

		if ggpoerr == nil {
			if !rs.simulateFrame(s) {
				return false
			}

			defer func() {
				if re := recover(); re != nil {
					if rs.session.config.DesyncTest {
						rs.session.log.updateLogs()
						rs.session.log.saveLogs()
						panic("RaiseDesyncError")
					}
				}
			}()

			err := rs.session.backend.AdvanceFrame(rs.session.LiveChecksum(s))
			if err != nil {
				panic(err)
			}
		}
	}

	return true
}

func (rs *RollbackSystem) runShortcutScripts(s *System) {
	for _, v := range s.shortcutScripts {
		if v.Activate {
			if err := s.luaLState.DoString(v.Script); err != nil {
				s.errLog.Println(err.Error())
			}
		}
	}
}

// Contains the logic for a single frame of the game
// Called by both runFrame for speculative execution, and AdvanceFrame for confirmed execution
func (rs *RollbackSystem) simulateFrame(s *System) bool {
	s.step = false
	//rs.runShortcutScripts(s)

	// If next round
	if !rs.runNextRound(s) {
		return false
	}

	s.bgPalFX.step()
	s.stage.action()

	// If frame is ready to tick and not paused
	//rs.updateStage(s)

	// Update game state
	s.action()

	// if rs.handleFlags(s) {
	// 	return true
	// }

	if !rs.updateEvents(s) {
		return false
	}

	// Break if finished
	if rs.currentFight.fin && (!s.postMatchFlg || len(s.cfg.Common.Lua) == 0) {
		return false
	}

	// Update system; break if update returns false (game ended)
	//if !s.update() {
	//	return false
	//}

	// If end match selected from menu/end of attract mode match/etc
	if s.endMatch {
		s.esc = true
		return false
	} else if s.esc {
		s.endMatch = s.netConnection != nil || len(s.cfg.Common.Lua) == 0
		return false
	}
	return true
}

func (rs *RollbackSystem) runNextRound(s *System) bool {
	if s.roundOver() && !rs.currentFight.fin {
		s.round++
		for i := range s.roundsExisted {
			s.roundsExisted[i]++
		}
		s.clearAllSound()
		tbl_roundNo := s.luaLState.NewTable()
		for _, p := range s.chars {
			if len(p) > 0 && p[0].teamside != -1 {
				tmp := s.luaLState.NewTable()
				tmp.RawSetString("name", lua.LString(p[0].name))
				tmp.RawSetString("id", lua.LNumber(p[0].id))
				tmp.RawSetString("memberNo", lua.LNumber(p[0].memberNo))
				tmp.RawSetString("selectNo", lua.LNumber(p[0].selectNo))
				tmp.RawSetString("teamside", lua.LNumber(p[0].teamside))
				tmp.RawSetString("life", lua.LNumber(p[0].life))
				tmp.RawSetString("lifeMax", lua.LNumber(p[0].lifeMax))
				tmp.RawSetString("winquote", lua.LNumber(p[0].winquote))
				tmp.RawSetString("aiLevel", lua.LNumber(p[0].getAILevel()))
				tmp.RawSetString("palno", lua.LNumber(p[0].gi().palno))
				tmp.RawSetString("ratiolevel", lua.LNumber(p[0].ocd().ratioLevel))
				tmp.RawSetString("win", lua.LBool(p[0].win()))
				tmp.RawSetString("winKO", lua.LBool(p[0].winKO()))
				tmp.RawSetString("winTime", lua.LBool(p[0].winTime()))
				tmp.RawSetString("winPerfect", lua.LBool(p[0].winPerfect()))
				tmp.RawSetString("winSpecial", lua.LBool(p[0].winType(WT_Special)))
				tmp.RawSetString("winHyper", lua.LBool(p[0].winType(WT_Hyper)))
				tmp.RawSetString("drawgame", lua.LBool(p[0].drawgame()))
				tmp.RawSetString("ko", lua.LBool(p[0].scf(SCF_ko)))
				tmp.RawSetString("over_ko", lua.LBool(p[0].scf(SCF_over_ko)))
				tbl_roundNo.RawSetInt(p[0].playerNo+1, tmp)
			}
		}
		s.matchData.RawSetInt(int(s.round-1), tbl_roundNo)
		s.scoreRounds = append(s.scoreRounds, [2]float32{s.lifebar.sc[0].scorePoints, s.lifebar.sc[1].scorePoints})
		rs.currentFight.oldTeamLeader = s.teamLeader

		if !s.matchOver() && (s.tmode[0] != TM_Turns || s.chars[0][0].win()) &&
			(s.tmode[1] != TM_Turns || s.chars[1][0].win()) {
			/* Prepare for the next round */
			for i, p := range s.chars {
				if len(p) > 0 {
					if s.tmode[i&1] != TM_Turns || !p[0].win() {
						p[0].life = p[0].lifeMax
					} else if p[0].life <= 0 {
						p[0].life = 1
					}
					p[0].redLife = 0
					rs.currentFight.copyVar(i)
				}
			}
			rs.currentFight.oldWins, rs.currentFight.oldDraws = s.wins, s.draws
			rs.currentFight.oldStageVars.copyStageVars(s.stage)
			rs.currentFight.reset()
		} else {
			/* End match, or prepare for a new character in turns mode */
			for i, tm := range s.tmode {
				if s.chars[i][0].win() || !s.chars[i][0].lose() && tm != TM_Turns {
					for j := i; j < len(s.chars); j += 2 {
						if len(s.chars[j]) > 0 {
							if s.chars[j][0].win() {
								s.chars[j][0].life = Max(1, int32(math.Ceil(math.Pow(rs.currentFight.lvmul,
									float64(rs.currentFight.level[i]))*float64(s.chars[j][0].life))))
							} else {
								s.chars[j][0].life = Max(1, s.cgi[j].data.life)
							}
						}
					}
					//} else {
					//	s.chars[i][0].life = 0
				}
			}
			// If match isn't over, presumably this is turns mode,
			// so break to restart fight for the next character
			if !s.matchOver() {
				return false
			}

			// Otherwise match is over
			s.postMatchFlg = true
			rs.currentFight.fin = true
		}
	}

	return true

}

func (rs *RollbackSystem) updateStage(s *System) {
	// Update stage
	s.stage.action()
}

func (rs *RollbackSystem) handleFlags(s *System) bool {
	// F4 pressed to restart round
	if s.roundResetFlg && !s.postMatchFlg {
		rs.currentFight.reset()
	}
	// Shift+F4 pressed to restart match
	if s.reloadFlg {
		return true
	}
	return false

}

func (rs *RollbackSystem) updateEvents(s *System) bool {
	if !s.addFrameTime(s.turbo) {
		if !s.eventUpdate() {
			return false
		}
		return false
	}
	return true
}

func (rs *RollbackSystem) updateCamera(s *System) {
	if !s.frameSkip {
		scl := s.cam.Scale / s.cam.BaseScale()
		if s.enableZoomtime > 0 {
			if !s.debugPaused() {
				s.zoomPosXLag += ((s.zoomPos[0] - s.zoomPosXLag) * (1 - s.zoomlag))
				s.zoomPosYLag += ((s.zoomPos[1] - s.zoomPosYLag) * (1 - s.zoomlag))
				s.drawScale = s.drawScale / (s.drawScale + (s.zoomScale*scl-s.drawScale)*s.zoomlag) * s.zoomScale * scl
			}
		} else {
			s.zoomlag = 0
			s.zoomPosXLag = 0
			s.zoomPosYLag = 0
			s.zoomScale = 1
			s.zoomPos = [2]float32{0, 0}
			s.drawScale = s.cam.Scale
		}
	}
}

func getAIInputs(player int) []byte {
	var ib InputBits
	ib.SetInputAI(player)
	return writeI32(int32(ib))
}

func (ib *InputBits) SetInputAI(in int) {
	*ib = InputBits(Btoi(sys.aiInput[in].U()) |
		Btoi(sys.aiInput[in].D())<<1 |
		Btoi(sys.aiInput[in].L())<<2 |
		Btoi(sys.aiInput[in].R())<<3 |
		Btoi(sys.aiInput[in].a())<<4 |
		Btoi(sys.aiInput[in].b())<<5 |
		Btoi(sys.aiInput[in].c())<<6 |
		Btoi(sys.aiInput[in].x())<<7 |
		Btoi(sys.aiInput[in].y())<<8 |
		Btoi(sys.aiInput[in].z())<<9 |
		Btoi(sys.aiInput[in].s())<<10 |
		Btoi(sys.aiInput[in].d())<<11 |
		Btoi(sys.aiInput[in].w())<<12 |
		Btoi(sys.aiInput[in].m())<<13)
}

// system.go already refactored to use a local var to save all this data
type Fight struct {
	fin                                                               bool
	oldTeamLeader                                                     [2]int
	oldWins                                                           [2]int32
	oldDraws                                                          int32
	oldStageVars                                                      Stage
	level                                                             []int32
	lvmul                                                             float64
	life, pow, gpow, spow, rlife                                      []int32
	cnsvar                                                            []map[int32]int32
	cnsfvar                                                           []map[int32]float32
	dialogue                                                          [][]string
	mapArray                                                          []map[string]float32
	remapSpr                                                          []RemapPreset
	lifeMax, power, powerMax                                          []int32
	guardPoints, guardPointsMax, dizzyPoints, dizzyPointsMax, redLife []int32
	teamside                                                          []int
}

func (f *Fight) copyVar(pn int) {
	f.life[pn] = sys.chars[pn][0].life
	f.lifeMax[pn] = sys.chars[pn][0].lifeMax
	f.power[pn] = sys.chars[pn][0].power
	f.powerMax[pn] = sys.chars[pn][0].powerMax
	f.guardPoints[pn] = sys.chars[pn][0].guardPoints
	f.guardPointsMax[pn] = sys.chars[pn][0].guardPointsMax
	f.dizzyPoints[pn] = sys.chars[pn][0].dizzyPoints
	f.dizzyPointsMax[pn] = sys.chars[pn][0].dizzyPointsMax
	f.redLife[pn] = sys.chars[pn][0].redLife
	f.teamside[pn] = sys.chars[pn][0].teamside

	f.cnsvar[pn] = make(map[int32]int32)
	for k, v := range sys.chars[pn][0].cnsvar {
		f.cnsvar[pn][k] = v
	}

	f.cnsfvar[pn] = make(map[int32]float32)
	for k, v := range sys.chars[pn][0].cnsfvar {
		f.cnsfvar[pn][k] = v
	}

	f.mapArray[pn] = make(map[string]float32)
	for k, v := range sys.chars[pn][0].mapArray {
		f.mapArray[pn][k] = v
	}

	copy(f.dialogue[pn], sys.chars[pn][0].dialogue[:])

	f.remapSpr[pn] = make(RemapPreset)
	for k, v := range sys.chars[pn][0].remapSpr {
		f.remapSpr[pn][k] = v
	}
}

func (f *Fight) reset() {
	sys.wins, sys.draws = f.oldWins, f.oldDraws
	sys.teamLeader = f.oldTeamLeader
	for i, p := range sys.chars {
		if len(p) > 0 {
			p[0].life = f.life[i]
			p[0].lifeMax = f.lifeMax[i]
			p[0].power = f.power[i]
			p[0].powerMax = f.powerMax[i]
			p[0].guardPoints = f.guardPoints[i]
			p[0].guardPointsMax = f.guardPointsMax[i]
			p[0].dizzyPoints = f.dizzyPoints[i]
			p[0].dizzyPointsMax = f.dizzyPointsMax[i]
			p[0].redLife = f.redLife[i]
			p[0].teamside = f.teamside[i]
			p[0].cnsvar = make(map[int32]int32)
			for k, v := range f.cnsvar[i] {
				p[0].cnsvar[k] = v
			}
			p[0].cnsfvar = make(map[int32]float32)
			for k, v := range f.cnsfvar[i] {
				p[0].cnsfvar[k] = v
			}
			copy(p[0].dialogue[:], f.dialogue[i])
			p[0].mapArray = make(map[string]float32)
			for k, v := range f.mapArray[i] {
				p[0].mapArray[k] = v
			}
			p[0].remapSpr = make(RemapPreset)
			for k, v := range f.remapSpr[i] {
				p[0].remapSpr[k] = v
			}
		}
	}
	sys.stage.copyStageVars(&f.oldStageVars)
	sys.resetFrameTime()
	sys.nextRound()
	sys.roundResetFlg, sys.introSkipped = false, false
	sys.reloadFlg, sys.reloadStageFlg, sys.reloadLifebarFlg = false, false, false
	sys.runMainThreadTask()
	gfx.Await()
}

func (f *Fight) endFight() {
	sys.oldNextAddTime = 1
	sys.nomusic = false
	sys.allPalFX.clear()
	sys.allPalFX.enable = false
	for i, p := range sys.chars {
		if len(p) > 0 {
			sys.clearPlayerAssets(i, sys.matchOver() || (sys.tmode[i&1] == TM_Turns && p[0].life <= 0))
		}
	}
	sys.wincnt.update()
}

func (f *Fight) initChars() {
	// Initialize each character
	lvmul := math.Pow(2, 1.0/12)
	for i, p := range sys.chars {
		if len(p) > 0 {
			// Get max life, and adjust based on team mode
			var lm float32
			if p[0].ocd().lifeMax != -1 {
				lm = float32(p[0].ocd().lifeMax) * p[0].ocd().lifeRatio * sys.cfg.Options.Life / 100
			} else {
				lm = float32(p[0].gi().data.life) * p[0].ocd().lifeRatio * sys.cfg.Options.Life / 100
			}
			if p[0].teamside != -1 {
				switch sys.tmode[i&1] {
				case TM_Single:
					switch sys.tmode[(i+1)&1] {
					case TM_Simul, TM_Tag:
						lm *= sys.cfg.Options.Team.SingleVsTeamLife / 100
					case TM_Turns:
						if sys.numTurns[(i+1)&1] < sys.matchWins[(i+1)&1] && sys.cfg.Options.Team.LifeShare {
							lm = lm * float32(sys.numTurns[(i+1)&1]) /
								float32(sys.matchWins[(i+1)&1])
						}
					}
				case TM_Simul, TM_Tag:
					switch sys.tmode[(i+1)&1] {
					case TM_Simul, TM_Tag:
						if sys.numSimul[(i+1)&1] < sys.numSimul[i&1] && sys.cfg.Options.Team.LifeShare {
							lm = lm * float32(sys.numSimul[(i+1)&1]) / float32(sys.numSimul[i&1])
						}
					case TM_Turns:
						if sys.numTurns[(i+1)&1] < sys.numSimul[i&1]*sys.matchWins[(i+1)&1] && sys.cfg.Options.Team.LifeShare {
							lm = lm * float32(sys.numTurns[(i+1)&1]) /
								float32(sys.numSimul[i&1]*sys.matchWins[(i+1)&1])
						}
					default:
						if sys.cfg.Options.Team.LifeShare {
							lm /= float32(sys.numSimul[i&1])
						}
					}
				case TM_Turns:
					switch sys.tmode[(i+1)&1] {
					case TM_Single:
						if sys.matchWins[i&1] < sys.numTurns[i&1] && sys.cfg.Options.Team.LifeShare {
							lm = lm * float32(sys.matchWins[i&1]) / float32(sys.numTurns[i&1])
						}
					case TM_Simul, TM_Tag:
						if sys.numSimul[(i+1)&1]*sys.matchWins[i&1] < sys.numTurns[i&1] && sys.cfg.Options.Team.LifeShare {
							lm = lm * sys.cfg.Options.Team.SingleVsTeamLife / 100 *
								float32(sys.numSimul[(i+1)&1]*sys.matchWins[i&1]) /
								float32(sys.numTurns[i&1])
						}
					case TM_Turns:
						if sys.numTurns[(i+1)&1] < sys.numTurns[i&1] && sys.cfg.Options.Team.LifeShare {
							lm = lm * float32(sys.numTurns[(i+1)&1]) / float32(sys.numTurns[i&1])
						}
					}
				}
			}
			foo := math.Pow(lvmul, float64(-f.level[i]))
			p[0].lifeMax = Max(1, int32(math.Floor(foo*float64(lm))))

			if p[0].roundsExisted() > 0 {
				// If character already existed for a round, presumably because of turns mode, just update life
				p[0].life = Min(p[0].lifeMax, int32(math.Ceil(foo*float64(p[0].life))))
			} else if sys.round == 1 || sys.tmode[i&1] == TM_Turns {
				// If round 1 or a new character in turns mode, initialize values
				if p[0].ocd().life != -1 {
					p[0].life = Clamp(p[0].ocd().life, 0, p[0].lifeMax)
					p[0].redLife = p[0].life
				} else {
					p[0].life = p[0].lifeMax
					p[0].redLife = p[0].lifeMax
				}
				if sys.round == 1 {
					if sys.maxPowerMode {
						p[0].power = p[0].powerMax
					} else if p[0].ocd().power != -1 {
						p[0].power = Clamp(p[0].ocd().power, 0, p[0].powerMax)
					} else if !sys.consecutiveRounds || sys.consecutiveWins[0] == 0 {
						p[0].power = 0
					}
				}
				p[0].power = Clamp(p[0].power, 0, p[0].powerMax) // Because of previous partner in Turns mode
				p[0].dialogue = []string{}
				p[0].mapArray = make(map[string]float32)
				for k, v := range p[0].mapDefault {
					p[0].mapArray[k] = v
				}
				p[0].remapSpr = make(RemapPreset)
			}

			if p[0].ocd().guardPoints != -1 {
				p[0].guardPoints = Clamp(p[0].ocd().guardPoints, 0, p[0].guardPointsMax)
			} else {
				p[0].guardPoints = p[0].guardPointsMax
			}
			if p[0].ocd().dizzyPoints != -1 {
				p[0].dizzyPoints = Clamp(p[0].ocd().dizzyPoints, 0, p[0].dizzyPointsMax)
			} else {
				p[0].dizzyPoints = p[0].dizzyPointsMax
			}
			f.copyVar(i)
		}
	}
}

func (f *Fight) initSuperMeter() {
	// Prepare next round for all players
	for _, p := range sys.chars {
		if len(p) > 0 {
			p[0].prepareNextRound()
		}
	}

	for i, p := range sys.chars {
		if len(p) > 0 && p[0].teamside != -1 {
			f.level[i] = sys.wincnt.getLevel(i)
			if sys.cfg.Options.Team.PowerShare {
				pmax := Max(sys.cgi[i&1].data.power, sys.cgi[i].data.power)
				for j := i & 1; j < MaxSimul*2; j += 2 {
					if len(sys.chars[j]) > 0 {
						sys.chars[j][0].powerMax = pmax
					}
				}
			}
		}
	}
}

func (f *Fight) initTeamsLevels() {
	minlv, maxlv := f.level[0], f.level[0]
	for i, lv := range f.level[1:] {
		if len(sys.chars[i+1]) > 0 {
			minlv = Min(minlv, lv)
			maxlv = Max(maxlv, lv)
		}
	}
	if minlv > 0 {
		for i := range f.level {
			f.level[i] -= minlv
		}
	} else if maxlv < 0 {
		for i := range f.level {
			f.level[i] -= maxlv
		}
	}
}

func NewFight() Fight {
	f := Fight{}
	f.oldStageVars.copyStageVars(sys.stage)
	f.life = make([]int32, len(sys.chars))
	f.lifeMax = make([]int32, len(sys.chars))
	f.pow = make([]int32, len(sys.chars))
	f.gpow = make([]int32, len(sys.chars))
	f.spow = make([]int32, len(sys.chars))
	f.rlife = make([]int32, len(sys.chars))
	f.cnsvar = make([]map[int32]int32, len(sys.chars))
	f.cnsfvar = make([]map[int32]float32, len(sys.chars))
	f.power = make([]int32, len(sys.chars))
	f.powerMax = make([]int32, len(sys.chars))
	f.guardPoints = make([]int32, len(sys.chars))
	f.guardPointsMax = make([]int32, len(sys.chars))
	f.dizzyPoints = make([]int32, len(sys.chars))
	f.dizzyPointsMax = make([]int32, len(sys.chars))
	f.redLife = make([]int32, len(sys.chars))
	f.teamside = make([]int, len(sys.chars))
	f.dialogue = make([][]string, len(sys.chars))
	f.mapArray = make([]map[string]float32, len(sys.chars))
	f.remapSpr = make([]RemapPreset, len(sys.chars))
	f.level = make([]int32, len(sys.chars))
	return f
}

func readI32(b []byte) int32 {
	if len(b) < 4 {
		return 0
	}
	return int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16 | int32(b[3])<<24
}

func decodeInputs(buffer [][]byte) []InputBits {
	var inputs = make([]InputBits, len(buffer))
	for i, b := range buffer {
		inputs[i] = InputBits(readI32(b))
	}
	return inputs
}

// HACK: So you won't be playing eachothers characters
func reverseInputs(inputs []InputBits) []InputBits {
	for i, j := 0, len(inputs)-1; i < j; i, j = i+1, j-1 {
		inputs[i], inputs[j] = inputs[j], inputs[i]
	}
	return inputs
}

func writeI32(i32 int32) []byte {
	b := []byte{byte(i32), byte(i32 >> 8), byte(i32 >> 16), byte(i32 >> 24)}
	return b
}

func (rs *RollbackSystem) getInputs(player int) []byte {
	var ib InputBits
	ib.KeysToBits(rs.netConnection.buf[player].InputReader.LocalInput(0, false))
	return writeI32(int32(ib))
}

func (rs *RollbackSystem) readRollbackInput(controller int) [14]bool {
	if controller < 0 || controller >= len(sys.inputRemap) {
		return [14]bool{}
	}

	remap := sys.inputRemap[controller]
	if remap < 0 || remap >= len(rs.ggpoInputs) {
		return [14]bool{}
	}

	return rs.ggpoInputs[remap].BitsToKeys()
}

func (rs *RollbackSystem) anyButton() bool {
	for _, b := range rs.ggpoInputs {
		if b&IB_anybutton != 0 {
			return true
		}
	}
	return false
}
