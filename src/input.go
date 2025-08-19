package main

import (
	"encoding/binary"
	"net"
	"os"
	"strings"
	"time"
)

var ModAlt = NewModifierKey(false, true, false)
var ModCtrlAlt = NewModifierKey(true, true, false)
var ModCtrlAltShift = NewModifierKey(true, true, true)

type CommandKey byte

const (
	CK_U CommandKey = iota
	CK_D
	CK_B
	CK_F
	CK_L
	CK_R
	CK_UB
	CK_UF
	CK_DB
	CK_DF
	CK_UL
	CK_UR
	CK_DL
	CK_DR
	CK_N
	CK_rU // r stands for release (~)
	CK_rD
	CK_rB
	CK_rF
	CK_rL
	CK_rR
	CK_rUB
	CK_rUF
	CK_rDB
	CK_rDF
	CK_rUL
	CK_rUR
	CK_rDL
	CK_rDR
	CK_rN
	CK_Us // s stands for sign ($)
	CK_Ds
	CK_Bs
	CK_Fs
	CK_Ls
	CK_Rs
	CK_UBs
	CK_UFs
	CK_DBs
	CK_DFs
	CK_ULs
	CK_URs
	CK_DLs
	CK_DRs
	CK_Ns
	CK_rUs // ~ and $ together
	CK_rDs
	CK_rBs
	CK_rFs
	CK_rLs
	CK_rRs
	CK_rUBs
	CK_rUFs
	CK_rDBs
	CK_rDFs
	CK_rULs
	CK_rURs
	CK_rDLs
	CK_rDRs
	CK_rNs
	CK_a
	CK_b
	CK_c
	CK_x
	CK_y
	CK_z
	CK_s
	CK_d
	CK_w
	CK_m
	CK_ra
	CK_rb
	CK_rc
	CK_rx
	CK_ry
	CK_rz
	CK_rs
	CK_rd
	CK_rw
	CK_rm
	CK_Last = CK_rm
)

func (ck CommandKey) IsDirectionPress() bool {
	return ck >= CK_U && ck < CK_rU || ck >= CK_Us && ck < CK_rUs
}

func (ck CommandKey) IsDirectionRelease() bool {
	return ck >= CK_rU && ck < CK_Us || ck >= CK_rUs && ck < CK_a
}

func (ck CommandKey) IsButtonPress() bool {
	return ck >= CK_a && ck < CK_ra
}

func (ck CommandKey) IsButtonRelease() bool {
	return ck >= CK_ra && ck <= CK_rm
}

type NetState int

const (
	NS_Stop NetState = iota
	NS_Playing
	NS_End
	NS_Stopped
	NS_Error
)

type ShortcutScript struct {
	Activate bool
	Script   string
	Pause    bool
	DebugKey bool
}

type ShortcutKey struct {
	Key Key
	Mod ModifierKey
}

func NewShortcutKey(key Key, ctrl, alt, shift bool) *ShortcutKey {
	sk := &ShortcutKey{}
	sk.Key = key
	sk.Mod = NewModifierKey(ctrl, alt, shift)
	return sk
}

func (sk ShortcutKey) Test(k Key, m ModifierKey) bool {
	return k == sk.Key && (m&ModCtrlAltShift) == sk.Mod
}

func OnKeyReleased(key Key, mk ModifierKey) {
	if key != KeyUnknown {
		sys.keyState[key] = false
		sys.keyInput = KeyUnknown
		sys.keyString = ""
	}
}

func OnKeyPressed(key Key, mk ModifierKey) {
	if key != KeyUnknown {
		sys.keyState[key] = true
		sys.keyInput = key
		sys.esc = sys.esc ||
			key == KeyEscape && (mk&ModCtrlAlt) == 0
		for k, v := range sys.shortcutScripts {
			if sys.netConnection == nil && (sys.replayFile == nil || !v.DebugKey) &&
				//(!sys.paused || sys.step || v.Pause) &&
				(sys.cfg.Debug.AllowDebugKeys || !v.DebugKey) {
				v.Activate = v.Activate || k.Test(key, mk)
			}
		}
		if key == KeyF12 {
			captureScreen()
		}
		if key == KeyEnter && (mk&ModAlt) != 0 {
			sys.window.toggleFullscreen()
		}
	}
}

func OnTextEntered(s string) {
	sys.keyString = s
}

func JoystickState(joy, button int) bool {
	if joy < 0 {
		return sys.keyState[Key(button)]
	}
	if joy >= input.GetMaxJoystickCount() {
		return false
	}
	axes := input.GetJoystickAxes(joy)
	if button >= 0 {
		// Query button state
		btns := input.GetJoystickButtons(joy)

		if button >= len(btns) {
			if len(btns) == 0 {
				return false
				// Prevent OOB errors #2141
			} else if len(axes) > 0 {
				if button == sys.joystickConfig[joy].dR {
					return axes[0] > sys.cfg.Input.ControllerStickSensitivity
				}
				if button == sys.joystickConfig[joy].dL {
					return -axes[0] > sys.cfg.Input.ControllerStickSensitivity
				}

				// Prevent OOB errors #2141
				if len(axes) > 1 {
					if button == sys.joystickConfig[joy].dU {
						return -axes[1] > sys.cfg.Input.ControllerStickSensitivity
					}
					if button == sys.joystickConfig[joy].dD {
						return axes[1] > sys.cfg.Input.ControllerStickSensitivity
					}
				}
				return false
			} else {
				return false
			}
		}

		// override with axes if they exist #2141
		if len(axes) > 0 {
			if button == sys.joystickConfig[joy].dR {
				if axes[0] > sys.cfg.Input.ControllerStickSensitivity {
					btns[button] = 1
				}
			}
			if button == sys.joystickConfig[joy].dL {
				if -axes[0] > sys.cfg.Input.ControllerStickSensitivity {
					btns[button] = 1
				}
			}

			// prevent OOB errors #2141
			if len(axes) > 1 {
				if button == sys.joystickConfig[joy].dU {
					if -axes[1] > sys.cfg.Input.ControllerStickSensitivity {
						btns[button] = 1
					}
				}
				if button == sys.joystickConfig[joy].dD {
					if axes[1] > sys.cfg.Input.ControllerStickSensitivity {
						btns[button] = 1
					}
				}
			}
		}

		return btns[button] != 0
	} else {
		// Query axis state
		axis := -button - 1
		if axis >= len(axes)*2 {
			return false
		}

		// Read value and invert sign for odd indices
		val := axes[axis/2] * float32((axis&1)*2-1)

		var joyName = input.GetJoystickName(joy)

		// Evaluate LR triggers on the Xbox 360 controller
		if (axis == 9 || axis == 11) && (strings.Contains(joyName, "XInput") || strings.Contains(joyName, "X360") ||
			strings.Contains(joyName, "Xbox Wireless") || strings.Contains(joyName, "Xbox Elite") || strings.Contains(joyName, "Xbox One") ||
			strings.Contains(joyName, "Xbox Series") || strings.Contains(joyName, "Xbox Adaptive")) {
			return val > sys.cfg.Input.XinputTriggerSensitivity
		}

		// Ignore trigger axis on PS4 (We already have buttons)
		if (axis >= 6 && axis <= 9) && joyName == "PS4 Controller" {
			return false
		}

		return val > sys.cfg.Input.ControllerStickSensitivity
	}
}

type KeyConfig struct {
	Joy, dU, dD, dL, dR, kA, kB, kC, kX, kY, kZ, kS, kD, kW, kM int
	GUID                                                        string
	isInitialized                                               bool
}

func (kc *KeyConfig) swap(kc2 *KeyConfig) {
	// joy := kc.Joy
	dD := kc.dD
	dL := kc.dL
	dR := kc.dR
	dU := kc.dU
	kA := kc.kA
	kB := kc.kB
	kC := kc.kC
	kD := kc.kD
	kW := kc.kW
	kX := kc.kX
	kY := kc.kY
	kZ := kc.kZ
	kM := kc.kM
	kS := kc.kS

	// kc.Joy = kc2.Joy
	kc.dD = kc2.dD
	kc.dL = kc2.dL
	kc.dR = kc2.dR
	kc.dU = kc2.dU
	kc.kA = kc2.kA
	kc.kB = kc2.kB
	kc.kC = kc2.kC
	kc.kD = kc2.kD
	kc.kW = kc2.kW
	kc.kX = kc2.kX
	kc.kY = kc2.kY
	kc.kZ = kc2.kZ
	kc.kM = kc2.kM
	kc.kS = kc2.kS

	// kc2.Joy = joy
	kc2.dD = dD
	kc2.dL = dL
	kc2.dR = dR
	kc2.dU = dU
	kc2.kA = kA
	kc2.kB = kB
	kc2.kC = kC
	kc2.kD = kD
	kc2.kW = kW
	kc2.kX = kX
	kc2.kY = kY
	kc2.kZ = kZ
	kc2.kM = kM
	kc2.kS = kS

	kc.isInitialized = true
	kc2.isInitialized = true
}

func (kc KeyConfig) U() bool { return JoystickState(kc.Joy, kc.dU) }
func (kc KeyConfig) D() bool { return JoystickState(kc.Joy, kc.dD) }
func (kc KeyConfig) L() bool { return JoystickState(kc.Joy, kc.dL) }
func (kc KeyConfig) R() bool { return JoystickState(kc.Joy, kc.dR) }
func (kc KeyConfig) a() bool { return JoystickState(kc.Joy, kc.kA) }
func (kc KeyConfig) b() bool { return JoystickState(kc.Joy, kc.kB) }
func (kc KeyConfig) c() bool { return JoystickState(kc.Joy, kc.kC) }
func (kc KeyConfig) x() bool { return JoystickState(kc.Joy, kc.kX) }
func (kc KeyConfig) y() bool { return JoystickState(kc.Joy, kc.kY) }
func (kc KeyConfig) z() bool { return JoystickState(kc.Joy, kc.kZ) }
func (kc KeyConfig) s() bool { return JoystickState(kc.Joy, kc.kS) }
func (kc KeyConfig) d() bool { return JoystickState(kc.Joy, kc.kD) }
func (kc KeyConfig) w() bool { return JoystickState(kc.Joy, kc.kW) }
func (kc KeyConfig) m() bool { return JoystickState(kc.Joy, kc.kM) }

type InputBits int32

const (
	IB_PU InputBits = 1 << iota
	IB_PD
	IB_PL
	IB_PR
	IB_A
	IB_B
	IB_C
	IB_X
	IB_Y
	IB_Z
	IB_S
	IB_D
	IB_W
	IB_M
	IB_anybutton = IB_A | IB_B | IB_C | IB_X | IB_Y | IB_Z | IB_S | IB_D | IB_W | IB_M
)

// Save local inputs as input bits to send or record
func (ibit *InputBits) KeysToBits(buttons [14]bool) {
	*ibit = InputBits(Btoi(buttons[0]) |
		Btoi(buttons[1])<<1 |
		Btoi(buttons[2])<<2 |
		Btoi(buttons[3])<<3 |
		Btoi(buttons[4])<<4 |
		Btoi(buttons[5])<<5 |
		Btoi(buttons[6])<<6 |
		Btoi(buttons[7])<<7 |
		Btoi(buttons[8])<<8 |
		Btoi(buttons[9])<<9 |
		Btoi(buttons[10])<<10 |
		Btoi(buttons[11])<<11 |
		Btoi(buttons[12])<<12 |
		Btoi(buttons[13])<<13)
}

// Convert received input bits back into keys
func (ibit InputBits) BitsToKeys() [14]bool {
	var U, D, L, R, a, b, c, x, y, z, s, d, w, m bool

	// Convert bits to logical symbols
	U = ibit&IB_PU != 0
	D = ibit&IB_PD != 0
	L = ibit&IB_PL != 0
	R = ibit&IB_PR != 0
	a = ibit&IB_A != 0
	b = ibit&IB_B != 0
	c = ibit&IB_C != 0
	x = ibit&IB_X != 0
	y = ibit&IB_Y != 0
	z = ibit&IB_Z != 0
	s = ibit&IB_S != 0
	d = ibit&IB_D != 0
	w = ibit&IB_W != 0
	m = ibit&IB_M != 0

	return [14]bool{U, D, L, R, a, b, c, x, y, z, s, d, w, m}
}

type CommandKeyRemap struct {
	a, b, c, x, y, z, s, d, w, m, na, nb, nc, nx, ny, nz, ns, nd, nw, nm CommandKey
}

func NewCommandKeyRemap() *CommandKeyRemap {
	return &CommandKeyRemap{CK_a, CK_b, CK_c, CK_x, CK_y, CK_z, CK_s, CK_d, CK_w, CK_m,
		CK_ra, CK_rb, CK_rc, CK_rx, CK_ry, CK_rz, CK_rs, CK_rd, CK_rw, CK_rm}
}

type InputReader struct {
	SocdAllow          [4]bool // Up, down, back, forward
	SocdFirst          [4]bool
	ButtonAssistBuffer [9]bool
}

func NewInputReader() *InputReader {
	return &InputReader{
		SocdAllow:          [4]bool{},
		SocdFirst:          [4]bool{},
		ButtonAssistBuffer: [9]bool{},
	}
}

func (ir *InputReader) LocalInput(in int, script bool) [14]bool {
	var U, D, L, R, a, b, c, x, y, z, s, d, w, m bool

	// Keyboard
	if in < len(sys.keyConfig) {
		joy := sys.keyConfig[in].Joy
		if joy == -1 {
			U = sys.keyConfig[in].U()
			D = sys.keyConfig[in].D()
			L = sys.keyConfig[in].L()
			R = sys.keyConfig[in].R()
			a = sys.keyConfig[in].a()
			b = sys.keyConfig[in].b()
			c = sys.keyConfig[in].c()
			x = sys.keyConfig[in].x()
			y = sys.keyConfig[in].y()
			z = sys.keyConfig[in].z()
			s = sys.keyConfig[in].s()
			d = sys.keyConfig[in].d()
			w = sys.keyConfig[in].w()
			m = sys.keyConfig[in].m()
		}
	}

	// Joystick
	if in < len(sys.joystickConfig) {
		joyS := sys.joystickConfig[in].Joy
		if joyS >= 0 {
			U = U || sys.joystickConfig[in].U() // Does not override keyboard
			D = D || sys.joystickConfig[in].D()
			L = L || sys.joystickConfig[in].L()
			R = R || sys.joystickConfig[in].R()
			a = a || sys.joystickConfig[in].a()
			b = b || sys.joystickConfig[in].b()
			c = c || sys.joystickConfig[in].c()
			x = x || sys.joystickConfig[in].x()
			y = y || sys.joystickConfig[in].y()
			z = z || sys.joystickConfig[in].z()
			s = s || sys.joystickConfig[in].s()
			d = d || sys.joystickConfig[in].d()
			w = w || sys.joystickConfig[in].w()
			m = m || sys.joystickConfig[in].m()
		}
	}

	// Button assist is checked locally so that the sent inputs are already processed
	if sys.cfg.Input.ButtonAssist {
		if script {
			ir.ButtonAssistBuffer = [9]bool{}
		} else {
			result := ir.ButtonAssistCheck([9]bool{a, b, c, x, y, z, s, d, w})
			a, b, c, x, y, z, s, d, w = result[0], result[1], result[2], result[3], result[4], result[5], result[6], result[7], result[8]
		}
	}

	return [14]bool{U, D, L, R, a, b, c, x, y, z, s, d, w, m}
}

// Resolve Simultaneous Opposing Cardinal Directions (SOCD)
// Left and Right are solved in CommandList Input based on B and F outcome
func (ir *InputReader) SocdResolution(U, D, B, F bool) (bool, bool, bool, bool) {
	method := sys.cfg.Input.SOCDResolution

	// Neutral resolution is enforced during netplay
	// Note: Since configuration does not work online yet, it's best if the forced setting matches the default config
	if sys.netConnection != nil || sys.replayFile != nil {
		method = 4
	}

	// Resolve U and D conflicts based on SOCD resolution config
	resolveUD := func(U, D bool) (bool, bool) {
		// Check first direction held
		if method == 1 || method == 3 {
			if U || D {
				if !U {
					ir.SocdFirst[0] = false
				}
				if !D {
					ir.SocdFirst[1] = false
				}
				if !ir.SocdFirst[0] && !ir.SocdFirst[1] {
					if D {
						ir.SocdFirst[1] = true
					} else {
						ir.SocdFirst[0] = true
					}
				}
			} else {
				ir.SocdFirst[0] = false
				ir.SocdFirst[1] = false
			}
		}
		// Apply SOCD resolution according to config
		if D && U {
			switch method {
			case 0: // Allow both directions (no resolution)
				ir.SocdAllow[0] = true
				ir.SocdAllow[1] = true
			case 1: // Last direction priority
				if ir.SocdFirst[0] {
					ir.SocdAllow[0] = false
					ir.SocdAllow[1] = true
				} else {
					ir.SocdAllow[0] = true
					ir.SocdAllow[1] = false
				}
			case 2: // Absolute priority (offense over defense)
				ir.SocdAllow[0] = true
				ir.SocdAllow[1] = false
			case 3: // First direction priority
				if ir.SocdFirst[0] {
					ir.SocdAllow[0] = true
					ir.SocdAllow[1] = false
				} else {
					ir.SocdAllow[0] = false
					ir.SocdAllow[1] = true
				}
			default: // Deny either direction (neutral resolution)
				ir.SocdAllow[0] = false
				ir.SocdAllow[1] = false
			}
		} else {
			ir.SocdAllow[0] = true
			ir.SocdAllow[1] = true
		}

		return U, D
	}

	// Resolve B and F conflicts based on SOCD resolution config
	resolveBF := func(B, F bool) (bool, bool) {
		// Check first direction held
		if method == 1 || method == 3 {
			if B || F {
				if !B {
					ir.SocdFirst[2] = false
				}
				if !F {
					ir.SocdFirst[3] = false
				}
				if !ir.SocdFirst[2] && !ir.SocdFirst[3] {
					if B {
						ir.SocdFirst[2] = true
					} else {
						ir.SocdFirst[3] = true
					}
				}
			} else {
				ir.SocdFirst[2] = false
				ir.SocdFirst[3] = false
			}
		}
		// Apply SOCD resolution according to config
		if B && F {
			switch method {
			case 0: // Allow both directions (no resolution)
				ir.SocdAllow[2] = true
				ir.SocdAllow[3] = true
			case 1: // Last direction priority
				if ir.SocdFirst[3] {
					ir.SocdAllow[2] = true
					ir.SocdAllow[3] = false
				} else {
					ir.SocdAllow[2] = false
					ir.SocdAllow[3] = true
				}
			case 2: // Absolute priority (offense over defense)
				ir.SocdAllow[2] = false
				ir.SocdAllow[3] = true
			case 3: // First direction priority
				if ir.SocdFirst[3] {
					ir.SocdAllow[2] = false
					ir.SocdAllow[3] = true
				} else {
					ir.SocdAllow[2] = true
					ir.SocdAllow[3] = false
				}
			default: // Deny either direction (neutral resolution)
				ir.SocdAllow[2] = false
				ir.SocdAllow[3] = false
			}
		} else {
			ir.SocdAllow[2] = true
			ir.SocdAllow[3] = true
		}

		return B, F
	}

	// Resolve up and down
	U, D = resolveUD(U, D)
	// Resolve back and forward
	B, F = resolveBF(B, F)
	// Apply resulting resolution
	U = U && ir.SocdAllow[0]
	D = D && ir.SocdAllow[1]
	B = B && ir.SocdAllow[2]
	F = F && ir.SocdAllow[3]

	return U, D, B, F
}

// Add extra frame of leniency when checking button presses
func (ir *InputReader) ButtonAssistCheck(curr [9]bool) [9]bool {
	var result [9]bool

	// Check if any button was pressed in the previous frame
	prev := false
	for i := range ir.ButtonAssistBuffer {
		if ir.ButtonAssistBuffer[i] {
			prev = true
			break
		}
	}

	// Check both current and previous frame if any button was pressed in the previous frame
	// Otherwise just use the previous frame's buttons
	for i := range ir.ButtonAssistBuffer {
		result[i] = ir.ButtonAssistBuffer[i] || (curr[i] && prev)
	}

	// Save current frame's buttons to be checked in the next frame
	ir.ButtonAssistBuffer = curr

	return result
}

type InputBuffer struct {
	Bb, Db, Fb, Ub, Lb, Rb, Nb             int32
	ab, bb, cb, xb, yb, zb, sb, db, wb, mb int32
	B, D, F, U, L, R, N                    int8
	a, b, c, x, y, z, s, d, w, m           int8
	InputReader                            *InputReader
}

func NewInputBuffer() (c *InputBuffer) {
	ir := NewInputReader()
	c = &InputBuffer{InputReader: ir}
	c.Reset()
	return c
}

func (c *InputBuffer) Reset() {
	*c = InputBuffer{
		B: -1, D: -1, F: -1, U: -1, L: -1, R: -1, N: 1, // Set directions to released state
		a: -1, b: -1, c: -1, x: -1, y: -1, z: -1, s: -1, d: -1, w: -1, m: -1, // Set buttons to released state
		InputReader: NewInputReader(),
	}
}

// Updates how long ago a char pressed or released a button
func (__ *InputBuffer) updateInputTime(U, D, L, R, B, F, a, b, c, x, y, z, s, d, w, m bool) {
	// SOCD resolution is now handled beforehand, so that it may be easier to port to netplay later
	if U != (__.U > 0) { // If button state changed, set buffer to 0 and invert the state
		__.Ub = 0
		__.U *= -1
	}
	__.Ub += int32(__.U) // Increment buffer time according to button state
	if D != (__.D > 0) {
		__.Db = 0
		__.D *= -1
	}
	__.Db += int32(__.D)
	if L != (__.L > 0) {
		__.Lb = 0
		__.L *= -1
	}
	__.Lb += int32(__.L)
	if R != (__.R > 0) {
		__.Rb = 0
		__.R *= -1
	}
	__.Rb += int32(__.R)
	if B != (__.B > 0) {
		__.Bb = 0
		__.B *= -1
	}
	__.Bb += int32(__.B)
	if F != (__.F > 0) {
		__.Fb = 0
		__.F *= -1
	}
	__.Fb += int32(__.F)
	// Neutral
	if (!U && !D && !F && !B) != (__.N > 0) {
		__.Nb = 0
		__.N *= -1
	}
	__.Nb += int32(__.N)
	// Buttons
	if a != (__.a > 0) {
		__.ab = 0
		__.a *= -1
	}
	__.ab += int32(__.a)
	if b != (__.b > 0) {
		__.bb = 0
		__.b *= -1
	}
	__.bb += int32(__.b)
	if c != (__.c > 0) {
		__.cb = 0
		__.c *= -1
	}
	__.cb += int32(__.c)
	if x != (__.x > 0) {
		__.xb = 0
		__.x *= -1
	}
	__.xb += int32(__.x)
	if y != (__.y > 0) {
		__.yb = 0
		__.y *= -1
	}
	__.yb += int32(__.y)
	if z != (__.z > 0) {
		__.zb = 0
		__.z *= -1
	}
	__.zb += int32(__.z)
	if s != (__.s > 0) {
		__.sb = 0
		__.s *= -1
	}
	__.sb += int32(__.s)
	if d != (__.d > 0) {
		__.db = 0
		__.d *= -1
	}
	__.db += int32(__.d)
	if w != (__.w > 0) {
		__.wb = 0
		__.w *= -1
	}
	__.wb += int32(__.w)
	if m != (__.m > 0) {
		__.mb = 0
		__.m *= -1
	}
	__.mb += int32(__.m)
}

// Check buffer state of each key
func (__ *InputBuffer) State(ck CommandKey) int32 {
	switch ck {
	case CK_U:
		return Min(-Max(__.Bb, Max(__.Db, __.Fb)), __.Ub)
	case CK_D:
		return Min(-Max(__.Bb, Max(__.Ub, __.Fb)), __.Db)
	case CK_B:
		return Min(-Max(__.Db, Max(__.Ub, __.Fb)), __.Bb)
	case CK_F:
		return Min(-Max(__.Db, Max(__.Ub, __.Bb)), __.Fb)
	case CK_L:
		return Min(-Max(__.Db, Max(__.Ub, __.Rb)), __.Lb)
	case CK_R:
		return Min(-Max(__.Db, Max(__.Ub, __.Lb)), __.Rb)
	case CK_UF:
		return Min(Min(-__.Db, __.Ub), Min(-__.Bb, __.Fb))
	case CK_UB:
		return Min(Min(-__.Db, __.Ub), Min(-__.Fb, __.Bb))
	case CK_DF:
		return Min(Min(-__.Ub, __.Db), Min(-__.Bb, __.Fb))
	case CK_DB:
		return Min(Min(-__.Ub, __.Db), Min(-__.Fb, __.Bb))
	case CK_UL:
		return Min(Min(-__.Db, __.Ub), Min(-__.Rb, __.Lb))
	case CK_UR:
		return Min(Min(-__.Db, __.Ub), Min(-__.Lb, __.Rb))
	case CK_DL:
		return Min(Min(-__.Ub, __.Db), Min(-__.Rb, __.Lb))
	case CK_DR:
		return Min(Min(-__.Ub, __.Db), Min(-__.Lb, __.Rb))
	case CK_N, CK_Ns:
		return __.Nb
	case CK_Us:
		return __.Ub
	case CK_Ds:
		return __.Db
	case CK_Bs:
		return __.Bb
	case CK_Fs:
		return __.Fb
	case CK_Ls:
		return __.Lb
	case CK_Rs:
		return __.Rb
	case CK_UBs:
		return Min(__.Ub, __.Bb)
	case CK_UFs:
		return Min(__.Ub, __.Fb)
	case CK_DBs:
		return Min(__.Db, __.Bb)
	case CK_DFs:
		return Min(__.Db, __.Fb)
	case CK_ULs:
		return Min(__.Ub, __.Lb)
	case CK_URs:
		return Min(__.Ub, __.Rb)
	case CK_DLs:
		return Min(__.Db, __.Lb)
	case CK_DRs:
		return Min(__.Db, __.Rb)
	case CK_a:
		return __.ab
	case CK_b:
		return __.bb
	case CK_c:
		return __.cb
	case CK_x:
		return __.xb
	case CK_y:
		return __.yb
	case CK_z:
		return __.zb
	case CK_s:
		return __.sb
	case CK_d:
		return __.db
	case CK_w:
		return __.wb
	case CK_m:
		return __.mb
	case CK_rU:
		return -Min(-Max(__.Bb, Max(__.Db, __.Fb)), __.Ub)
	case CK_rD:
		return -Min(-Max(__.Bb, Max(__.Ub, __.Fb)), __.Db)
	case CK_rB:
		return -Min(-Max(__.Db, Max(__.Ub, __.Fb)), __.Bb)
	case CK_rF:
		return -Min(-Max(__.Db, Max(__.Ub, __.Bb)), __.Fb)
	case CK_rL:
		return -Min(-Max(__.Db, Max(__.Ub, __.Rb)), __.Lb)
	case CK_rR:
		return -Min(-Max(__.Db, Max(__.Ub, __.Lb)), __.Rb)
	case CK_rUF:
		return -Min(Min(-__.Db, __.Ub), Min(-__.Bb, __.Fb))
	case CK_rUB:
		return -Min(Min(-__.Db, __.Ub), Min(-__.Fb, __.Bb))
	case CK_rDF:
		return -Min(Min(-__.Ub, __.Db), Min(-__.Bb, __.Fb))
	case CK_rDB:
		return -Min(Min(-__.Ub, __.Db), Min(-__.Fb, __.Bb))
	case CK_rUL:
		return -Min(Min(-__.Db, __.Ub), Min(-__.Rb, __.Lb))
	case CK_rUR:
		return -Min(Min(-__.Db, __.Ub), Min(-__.Lb, __.Rb))
	case CK_rDL:
		return -Min(Min(-__.Ub, __.Db), Min(-__.Rb, __.Lb))
	case CK_rDR:
		return -Min(Min(-__.Ub, __.Db), Min(-__.Lb, __.Rb))
	case CK_rUs:
		return -__.Ub
	case CK_rDs:
		return -__.Db
	case CK_rBs:
		return -__.Bb
	case CK_rFs:
		return -__.Fb
	case CK_rLs:
		return -__.Lb
	case CK_rRs:
		return -__.Rb
	case CK_rUBs:
		return -Min(__.Ub, __.Bb)
	case CK_rUFs:
		return -Min(__.Ub, __.Fb)
	case CK_rDBs:
		return -Min(__.Db, __.Bb)
	case CK_rDFs:
		return -Min(__.Db, __.Fb)
	case CK_rULs:
		return -Min(__.Ub, __.Lb)
	case CK_rURs:
		return -Min(__.Ub, __.Rb)
	case CK_rDLs:
		return -Min(__.Db, __.Lb)
	case CK_rDRs:
		return -Min(__.Db, __.Rb)
	case CK_rN, CK_rNs:
		return -__.Nb
	case CK_ra:
		return -__.ab
	case CK_rb:
		return -__.bb
	case CK_rc:
		return -__.cb
	case CK_rx:
		return -__.xb
	case CK_ry:
		return -__.yb
	case CK_rz:
		return -__.zb
	case CK_rs:
		return -__.sb
	case CK_rd:
		return -__.db
	case CK_rw:
		return -__.wb
	case CK_rm:
		return -__.mb
	}
	return 0
}

// Check buffer state of each key
func (__ *InputBuffer) State2(ck CommandKey) int32 {
	f := func(a, b, c int32) int32 {
		switch {
		case a > 0:
			return -Max(b, c)
		case b > 0:
			return -Max(a, c)
		case c > 0:
			return -Max(a, b)
		}
		return -Max(a, b, c)
	}
	switch ck {
	case CK_Us:
		if __.Ub < 0 {
			return __.Ub
		}
		return Min(Abs(__.Ub), Abs(__.Bb), Abs(__.Fb))
	case CK_Ds:
		if __.Db < 0 {
			return __.Db
		}
		return Min(Abs(__.Db), Abs(__.Bb), Abs(__.Fb))
	case CK_Bs:
		if __.Bb < 0 {
			return __.Bb
		}
		return Min(Abs(__.Bb), Abs(__.Db), Abs(__.Ub))
	case CK_Fs:
		if __.Fb < 0 {
			return __.Fb
		}
		return Min(Abs(__.Fb), Abs(__.Db), Abs(__.Ub))
	case CK_Ls:
		if __.Lb < 0 {
			return __.Lb
		}
		return Min(Abs(__.Lb), Abs(__.Db), Abs(__.Ub))
	case CK_Rs:
		if __.Rb < 0 {
			return __.Rb
		}
		return Min(Abs(__.Rb), Abs(__.Db), Abs(__.Ub))
	// In MUGEN, adding '$' to diagonal inputs doesn't have any meaning.
	//case CK_DBs:
	//	if s := __.State(CK_DBs); s < 0 {
	//		return s
	//	}
	//	return Min(Abs(__.Db), Abs(__.Bb))
	//case CK_UBs:
	//	if s := __.State(CK_UBs); s < 0 {
	//		return s
	//	}
	//	return Min(Abs(__.Ub), Abs(__.Bb))
	//case CK_DFs:
	//	if s := __.State(CK_DFs); s < 0 {
	//		return s
	//	}
	//	return Min(Abs(__.Db), Abs(__.Fb))
	//case CK_UFs:
	//	if s := __.State(CK_UFs); s < 0 {
	//		return s
	//	}
	//	return Min(Abs(__.Ub), Abs(__.Fb))
	case CK_rUs:
		return f(__.State(CK_U), __.State(CK_UB), __.State(CK_UF))
	case CK_rDs:
		return f(__.State(CK_D), __.State(CK_DB), __.State(CK_DF))
	case CK_rBs:
		return f(__.State(CK_B), __.State(CK_UB), __.State(CK_DB))
	case CK_rFs:
		return f(__.State(CK_F), __.State(CK_DF), __.State(CK_UF))
	case CK_rLs:
		return f(__.State(CK_L), __.State(CK_UL), __.State(CK_DL))
	case CK_rRs:
		return f(__.State(CK_R), __.State(CK_DR), __.State(CK_UR))
		//case CK_rDBs:
		//	return f(__.State(CK_DB), __.State(CK_D), __.State(CK_B))
		//case CK_rUBs:
		//	return f(__.State(CK_UB), __.State(CK_U), __.State(CK_B))
		//case CK_rDFs:
		//	return f(__.State(CK_DF), __.State(CK_D), __.State(CK_F))
		//case CK_rUFs:
		//	return f(__.State(CK_UF), __.State(CK_U), __.State(CK_F))
	case CK_N:
		return __.State(CK_N)
	case CK_rN, CK_rNs:
		return __.State(CK_rN)
	case CK_Ns:
		return Min(Abs(__.Ub), Abs(__.Db), Abs(__.Bb), Abs(__.Fb), Abs(__.ab), Abs(__.bb), Abs(__.cb), Abs(__.xb), Abs(__.yb), Abs(__.zb), Abs(__.wb), Abs(__.db))
	}
	return __.State(ck)
}

// Time since last directional input was received
func (__ *InputBuffer) LastDirectionTime() int32 {
	return Min(Abs(__.Bb), Abs(__.Db), Abs(__.Fb), Abs(__.Ub), Abs(__.Lb), Abs(__.Rb))
}

// Time since last input was received. Used for ">" type commands
func (__ *InputBuffer) LastChangeTime() int32 {
	return Min(__.LastDirectionTime(), Abs(__.ab), Abs(__.bb), Abs(__.cb),
		Abs(__.xb), Abs(__.yb), Abs(__.zb), Abs(__.sb), Abs(__.db), Abs(__.wb),
		Abs(__.mb))
}

// NetBuffer holds the inputs that are sent between players
type NetBuffer struct {
	buf              [32]InputBits
	curT, inpT, senT int32
	InputReader      *InputReader
}

func (nb *NetBuffer) reset(time int32) {
	nb.curT, nb.inpT, nb.senT = time, time, time
	nb.InputReader = NewInputReader()
}

// Convert local player's key inputs into input bits for sending
func (nb *NetBuffer) writeNetBuffer(in int) {
	if nb.inpT-nb.curT < 32 {
		nb.buf[nb.inpT&31].KeysToBits(nb.InputReader.LocalInput(in, false))
		nb.inpT++
	}
}

// Read input bits from the net buffer
func (nb *NetBuffer) readNetBuffer() [14]bool {
	if nb.curT < nb.inpT {
		return nb.buf[nb.curT&31].BitsToKeys()
	}
	return [14]bool{}
}

// NetConnection manages the communication between players
type NetConnection struct {
	ln           *net.TCPListener
	conn         *net.TCPConn
	st           NetState
	sendEnd      chan bool
	recvEnd      chan bool
	buf          [MaxPlayerNo]NetBuffer
	locIn        int
	remIn        int
	time         int32
	stoppedcnt   int32
	delay        int32
	recording    *os.File
	host         bool
	preFightTime int32
}

func NewNetConnection() *NetConnection {
	nc := &NetConnection{st: NS_Stop,
		sendEnd: make(chan bool, 1), recvEnd: make(chan bool, 1)}
	nc.sendEnd <- true
	nc.recvEnd <- true
	return nc
}

func (nc *NetConnection) Close() {
	if nc.ln != nil {
		nc.ln.Close()
		nc.ln = nil
	}
	if nc.conn != nil {
		nc.conn.Close()
	}
	if nc.sendEnd != nil {
		<-nc.sendEnd
		close(nc.sendEnd)
		nc.sendEnd = nil
	}
	if nc.recvEnd != nil {
		<-nc.recvEnd
		close(nc.recvEnd)
		nc.recvEnd = nil
	}
	nc.conn = nil
}

func (nc *NetConnection) GetHostGuestRemap() (host, guest int) {
	host, guest = -1, -1
	for i, c := range sys.aiLevel {
		if c == 0 {
			if host < 0 {
				host = i
			} else if guest < 0 {
				guest = i
			}
		}
	}
	if host < 0 {
		host = 0
	}
	if guest < 0 {
		guest = (host + 1) % len(nc.buf)
	}
	return
}

func (nc *NetConnection) Accept(port string) error {
	if ln, err := net.Listen("tcp", ":"+port); err != nil {
		return err
	} else {
		nc.ln = ln.(*net.TCPListener)
		nc.host = true
		nc.locIn, nc.remIn = nc.GetHostGuestRemap()
		go func() {
			ln := nc.ln
			if conn, err := ln.AcceptTCP(); err == nil {
				nc.conn = conn
			}
			ln.Close()
		}()
	}
	return nil
}

func (nc *NetConnection) Connect(server, port string) {
	nc.host = false
	nc.remIn, nc.locIn = nc.GetHostGuestRemap()
	go func() {
		if conn, err := net.Dial("tcp", server+":"+port); err == nil {
			nc.conn = conn.(*net.TCPConn)
		}
	}()
}

func (nc *NetConnection) IsConnected() bool {
	return nc != nil && nc.conn != nil
}

func (nc *NetConnection) readNetInput(i int) [14]bool {
	if i >= 0 && i < len(nc.buf) {
		return nc.buf[sys.inputRemap[i]].readNetBuffer()
	}
	return [14]bool{}
}

func (nc *NetConnection) AnyButton() bool {
	for _, nb := range nc.buf {
		if nb.buf[nb.curT&31]&IB_anybutton != 0 {
			return true
		}
	}
	return false
}

func (nc *NetConnection) Stop() {
	if sys.esc {
		nc.end()
	} else {
		if nc.st != NS_End && nc.st != NS_Error {
			nc.st = NS_Stop
		}
		<-nc.sendEnd
		nc.sendEnd <- true
		<-nc.recvEnd
		nc.recvEnd <- true
	}
}

func (nc *NetConnection) end() {
	if nc.st != NS_Error {
		nc.st = NS_End
	}
	nc.Close()
}

func (nc *NetConnection) readI32() (int32, error) {
	b := [4]byte{}
	if _, err := nc.conn.Read(b[:]); err != nil {
		return 0, err
	}
	return int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16 | int32(b[3])<<24, nil
}

func (nc *NetConnection) writeI32(i32 int32) error {
	b := [...]byte{byte(i32), byte(i32 >> 8), byte(i32 >> 16), byte(i32 >> 24)}
	if _, err := nc.conn.Write(b[:]); err != nil {
		return err
	}
	return nil
}

func (nc *NetConnection) Synchronize() error {
	if !nc.IsConnected() || nc.st == NS_Error {
		return Error("Cannot connect to the other player")
	}
	nc.Stop()
	var seed int32
	if nc.host {
		seed = Random()
		if err := nc.writeI32(seed); err != nil {
			return err
		}
	} else {
		var err error
		if seed, err = nc.readI32(); err != nil {
			return err
		}
	}
	Srand(seed)
	var pfTime int32
	if nc.host {
		pfTime = sys.preFightTime
		if err := nc.writeI32(pfTime); err != nil {
			return err
		}
	} else {
		var err error
		if pfTime, err = nc.readI32(); err != nil {
			return err
		}
	}
	nc.preFightTime = pfTime
	if nc.recording != nil {
		binary.Write(nc.recording, binary.LittleEndian, &seed)
		binary.Write(nc.recording, binary.LittleEndian, &pfTime)
	}
	if err := nc.writeI32(nc.time); err != nil {
		return err
	}
	if tmp, err := nc.readI32(); err != nil {
		return err
	} else if tmp != nc.time {
		return Error("Synchronization error")
	}
	nc.buf[nc.locIn].reset(nc.time)
	nc.buf[nc.remIn].reset(nc.time)
	nc.st = NS_Playing
	<-nc.sendEnd
	go func(nb *NetBuffer) {
		defer func() { nc.sendEnd <- true }()
		for nc.st == NS_Playing {
			if nb.senT < nb.inpT {
				if err := nc.writeI32(int32(nb.buf[nb.senT&31])); err != nil {
					nc.st = NS_Error
					return
				}
				nb.senT++
			}
			time.Sleep(time.Millisecond)
		}
		nc.writeI32(-1)
	}(&nc.buf[nc.locIn])
	<-nc.recvEnd
	go func(nb *NetBuffer) {
		defer func() { nc.recvEnd <- true }()
		for nc.st == NS_Playing {
			if nb.inpT-nb.curT < 32 {
				if tmp, err := nc.readI32(); err != nil {
					nc.st = NS_Error
					return
				} else {
					nb.buf[nb.inpT&31] = InputBits(tmp)
					if tmp < 0 {
						nc.st = NS_Stopped
						return
					} else {
						nb.inpT++
						nb.senT = nb.inpT
					}
				}
			}
			time.Sleep(time.Millisecond)
		}
		for tmp := int32(0); tmp != -1; {
			var err error
			if tmp, err = nc.readI32(); err != nil {
				break
			}
		}
	}(&nc.buf[nc.remIn])
	nc.Update()
	return nil
}

func (nc *NetConnection) Update() bool {
	if nc.st != NS_Stopped {
		nc.stoppedcnt = 0
	}
	if !sys.gameEnd {
		switch nc.st {
		case NS_Stopped:
			nc.stoppedcnt++
			if nc.stoppedcnt > 60 {
				nc.st = NS_End
				break
			}
			fallthrough
		case NS_Playing:
			for {
				foo := Min(nc.buf[nc.locIn].senT, nc.buf[nc.remIn].senT)
				tmp := nc.buf[nc.remIn].inpT + nc.delay>>3 - nc.buf[nc.locIn].inpT
				if tmp >= 0 {
					nc.buf[nc.locIn].writeNetBuffer(0)
					if nc.delay > 0 {
						nc.delay--
					}
				} else if tmp < -1 {
					nc.delay += 4
				}
				if nc.time >= foo {
					if sys.esc || !sys.await(sys.cfg.Config.Framerate) || nc.st != NS_Playing {
						break
					}
					continue
				}
				nc.buf[nc.locIn].curT = nc.time
				nc.buf[nc.remIn].curT = nc.time
				if nc.recording != nil {
					for i := 0; i < MaxSimul*2; i++ {
						binary.Write(nc.recording, binary.LittleEndian, &nc.buf[i].buf[nc.time&31])
					}
				}
				nc.time++
				if nc.time >= foo {
					nc.buf[nc.locIn].writeNetBuffer(0)
				}
				break
			}
		case NS_End, NS_Error:
			sys.esc = true
		}
	}
	if sys.esc {
		nc.end()
	}
	return !sys.gameEnd
}

type ReplayFile struct {
	f      *os.File
	ibit   [MaxPlayerNo]InputBits
	pfTime int32
}

func OpenReplayFile(filename string) *ReplayFile {
	rf := &ReplayFile{}
	rf.f, _ = os.Open(filename)
	return rf
}

func (rf *ReplayFile) Close() {
	if rf.f != nil {
		rf.f.Close()
		rf.f = nil
	}
}

// Read input buttons from replay input
func (rf *ReplayFile) readReplayFile(i int) [14]bool {
	if i >= 0 && i < len(rf.ibit) {
		return rf.ibit[sys.inputRemap[i]].BitsToKeys()
	}
	return [14]bool{}
}

func (rf *ReplayFile) AnyButton() bool {
	for _, b := range rf.ibit {
		if b&IB_anybutton != 0 {
			return true
		}
	}
	return false
}

func (rf *ReplayFile) Synchronize() {
	if rf.f != nil {
		var seed int32
		if binary.Read(rf.f, binary.LittleEndian, &seed) == nil {
			Srand(seed)
		}
		var pfTime int32
		if binary.Read(rf.f, binary.LittleEndian, &pfTime) == nil {
			rf.pfTime = pfTime
			rf.Update()
		}
	}
}

func (rf *ReplayFile) Update() bool {
	if rf.f == nil {
		sys.esc = true
	} else {
		if sys.oldNextAddTime > 0 {
			for i := range rf.ibit {
				rf.ibit[i] = 0
			}
			err := binary.Read(rf.f, binary.LittleEndian, rf.ibit[:MaxSimul*2])
			if err != nil {
				sys.esc = true
			}
		}
		if sys.esc {
			rf.Close()
		}
	}
	return !sys.gameEnd
}

type AiInput struct {
	dir, dirt, at, bt, ct, xt, yt, zt, st, dt, wt, mt int32
}

func (ai *AiInput) Buttons() [14]bool {
	return [14]bool{
		ai.U(), ai.D(), ai.L(), ai.R(),
		ai.a(), ai.b(), ai.c(),
		ai.x(), ai.y(), ai.z(),
		ai.s(), ai.d(), ai.w(), ai.m(),
	}
}

// AI button jamming
func (ai *AiInput) Update(level float32) {
	// Not during intros and win poses
	if sys.intro != 0 {
		ai.dirt, ai.at, ai.bt, ai.ct = 0, 0, 0, 0
		ai.xt, ai.yt, ai.zt, ai.st = 0, 0, 0, 0
		ai.dt, ai.wt, ai.mt = 0, 0, 0
		return
	}
	var chance, time int32 = 15, 60
	jam := func(t *int32) bool {
		(*t)--
		if *t <= 0 {
			// TODO: Balance AI Scaling
			if Rand(1, chance) == 1 {
				*t = Rand(1, time)
				return true
			}
			*t = 0
		}
		return false
	}
	// Pick a random direction to press
	if jam(&ai.dirt) {
		ai.dir = Rand(0, 7)
	}
	chance, time = int32((-11.25*level+165)*7), 30
	jam(&ai.at)
	jam(&ai.bt)
	jam(&ai.ct)
	jam(&ai.xt)
	jam(&ai.yt)
	jam(&ai.zt)
	jam(&ai.dt)
	jam(&ai.wt)
	chance = 3600 // Start is jammed less often
	jam(&ai.st)
	//jam(&ai.mt) // We really don't need the AI to jam the menu button
}

// 0 = U, 1 = UR, 2 = R, 3 = DR, 4 = D, 5 = DL, 6 = L, 7 = UL
func (ai *AiInput) U() bool { return ai.dirt != 0 && (ai.dir == 7 || ai.dir == 0 || ai.dir == 1) }
func (ai *AiInput) D() bool { return ai.dirt != 0 && (ai.dir == 3 || ai.dir == 4 || ai.dir == 5) }
func (ai *AiInput) L() bool { return ai.dirt != 0 && (ai.dir == 5 || ai.dir == 6 || ai.dir == 7) }
func (ai *AiInput) R() bool { return ai.dirt != 0 && (ai.dir == 1 || ai.dir == 2 || ai.dir == 3) }
func (ai *AiInput) a() bool { return ai.at != 0 }
func (ai *AiInput) b() bool { return ai.bt != 0 }
func (ai *AiInput) c() bool { return ai.ct != 0 }
func (ai *AiInput) x() bool { return ai.xt != 0 }
func (ai *AiInput) y() bool { return ai.yt != 0 }
func (ai *AiInput) z() bool { return ai.zt != 0 }
func (ai *AiInput) s() bool { return ai.st != 0 }
func (ai *AiInput) d() bool { return ai.dt != 0 }
func (ai *AiInput) w() bool { return ai.wt != 0 }
func (ai *AiInput) m() bool { return ai.mt != 0 }

// cmdElem refers to each of the inputs required to complete a command
type cmdElem struct {
	key        []CommandKey
	chargetime int32
	slash      bool
	greater    bool
}

// Used to detect consecutive directions
func (ce *cmdElem) IsDirection() bool {
	// Released directions are not taken into account here
	return !ce.slash && len(ce.key) == 1 && ce.key[0].IsDirectionPress()
}

// Check if two command elements can be checked in the same frame
// This logic seems more complex in Mugen because of variable input delay
func (ce *cmdElem) IsDirToButton(next cmdElem) bool {
	// Not if second element must be held
	if next.slash {
		return false
	}
	// Not if first element includes button press or release
	for _, k := range ce.key {
		if k.IsButtonPress() || k.IsButtonRelease() {
			return false
		}
	}
	// Not if both elements share keys
	for _, k := range ce.key {
		for _, n := range next.key {
			if k == n {
				return false
			}
		}
	}
	// Yes if second element includes a button press
	for range ce.key {
		for _, n := range next.key {
			if n.IsButtonPress() {
				return true
			}
		}
	}
	// Yes if release direction then not release direction (includes buttons)
	for _, k := range ce.key {
		if k.IsDirectionRelease() {
			for _, n := range next.key {
				if !n.IsDirectionRelease() {
					return true
				}
			}
		}
	}
	return false
}

// Command refers to each individual command from the CMD file
type Command struct {
	name                   string
	hold                   [][]CommandKey
	held                   []bool
	cmd                    []cmdElem
	cmdidx, chargeidx      int
	maxtime, curtime       int32
	maxbuftime, curbuftime int32
	maxkeytime, curkeytime int32
	buffer_hitpause        bool
	buffer_pauseend        bool
	completeframe          bool
	hasSlash               bool
}

func newCommand() *Command {
	return &Command{chargeidx: -1, maxtime: 1, maxbuftime: 1, hasSlash: false}
}

// This is used to first compile the commands
func ReadCommand(name, cmdstr string, kr *CommandKeyRemap) (*Command, error) {
	c := newCommand()
	c.name = name
	cmd := strings.Split(cmdstr, ",")
	for _, cestr := range cmd {
		if len(c.cmd) > 0 && c.cmd[len(c.cmd)-1].slash {
			c.hold = append(c.hold, c.cmd[len(c.cmd)-1].key)
			c.cmd[len(c.cmd)-1] = cmdElem{chargetime: 1}
		} else {
			c.cmd = append(c.cmd, cmdElem{chargetime: 1})
		}
		ce := &c.cmd[len(c.cmd)-1]
		cestr = strings.TrimSpace(cestr)
		getChar := func() rune {
			if len(cestr) > 0 {
				return rune(cestr[0])
			}
			return rune(-1)
		}
		nextChar := func() rune {
			if len(cestr) > 0 {
				cestr = strings.TrimSpace(cestr[1:])
			}
			return getChar()
		}
		tilde := false
		switch getChar() {
		case '>':
			ce.greater = true
			r := nextChar()
			if r == '/' {
				ce.slash = true
				c.hasSlash = true
				nextChar()
				break
			} else if r == '~' {
			} else {
				break
			}
			fallthrough
		case '~':
			tilde = true
			n := int32(0)
			for r := nextChar(); '0' <= r && r <= '9'; r = nextChar() {
				n = n*10 + int32(r-'0')
			}
			if n > 0 {
				ce.chargetime = n
			}
		case '/':
			ce.slash = true
			c.hasSlash = true
			nextChar()
		}
		for len(cestr) > 0 {
			switch getChar() {
			case 'B':
				if tilde {
					ce.key = append(ce.key, CK_rB)
				} else {
					ce.key = append(ce.key, CK_B)
				}
				tilde = false
			case 'D':
				if len(cestr) > 1 && cestr[1] == 'B' {
					nextChar()
					if tilde {
						ce.key = append(ce.key, CK_rDB)
					} else {
						ce.key = append(ce.key, CK_DB)
					}
				} else if len(cestr) > 1 && cestr[1] == 'F' {
					nextChar()
					if tilde {
						ce.key = append(ce.key, CK_rDF)
					} else {
						ce.key = append(ce.key, CK_DF)
					}
				} else if len(cestr) > 1 && cestr[1] == 'L' {
					nextChar()
					if tilde {
						ce.key = append(ce.key, CK_rDL)
					} else {
						ce.key = append(ce.key, CK_DL)
					}
				} else if len(cestr) > 1 && cestr[1] == 'R' {
					nextChar()
					if tilde {
						ce.key = append(ce.key, CK_rDR)
					} else {
						ce.key = append(ce.key, CK_DR)
					}
				} else {
					if tilde {
						ce.key = append(ce.key, CK_rD)
					} else {
						ce.key = append(ce.key, CK_D)
					}
				}
				tilde = false
			case 'F':
				if tilde {
					ce.key = append(ce.key, CK_rF)
				} else {
					ce.key = append(ce.key, CK_F)
				}
				tilde = false
			case 'U':
				if len(cestr) > 1 && cestr[1] == 'B' {
					nextChar()
					if tilde {
						ce.key = append(ce.key, CK_rUB)
					} else {
						ce.key = append(ce.key, CK_UB)
					}
				} else if len(cestr) > 1 && cestr[1] == 'F' {
					nextChar()
					if tilde {
						ce.key = append(ce.key, CK_rUF)
					} else {
						ce.key = append(ce.key, CK_UF)
					}
				} else if len(cestr) > 1 && cestr[1] == 'L' {
					nextChar()
					if tilde {
						ce.key = append(ce.key, CK_rUL)
					} else {
						ce.key = append(ce.key, CK_UL)
					}
				} else if len(cestr) > 1 && cestr[1] == 'R' {
					nextChar()
					if tilde {
						ce.key = append(ce.key, CK_rUR)
					} else {
						ce.key = append(ce.key, CK_UR)
					}
				} else {
					if tilde {
						ce.key = append(ce.key, CK_rU)
					} else {
						ce.key = append(ce.key, CK_U)
					}
				}
				tilde = false
			case 'L':
				if tilde {
					ce.key = append(ce.key, CK_rL)
				} else {
					ce.key = append(ce.key, CK_L)
				}
				tilde = false
			case 'R':
				if tilde {
					ce.key = append(ce.key, CK_rR)
				} else {
					ce.key = append(ce.key, CK_R)
				}
				tilde = false
			case 'N':
				if tilde {
					ce.key = append(ce.key, CK_rN)
				} else {
					ce.key = append(ce.key, CK_N)
				}
				tilde = false
			case 'a':
				if tilde {
					ce.key = append(ce.key, kr.na)
				} else {
					ce.key = append(ce.key, kr.a)
				}
				tilde = false
			case 'b':
				if tilde {
					ce.key = append(ce.key, kr.nb)
				} else {
					ce.key = append(ce.key, kr.b)
				}
				tilde = false
			case 'c':
				if tilde {
					ce.key = append(ce.key, kr.nc)
				} else {
					ce.key = append(ce.key, kr.c)
				}
				tilde = false
			case 'x':
				if tilde {
					ce.key = append(ce.key, kr.nx)
				} else {
					ce.key = append(ce.key, kr.x)
				}
				tilde = false
			case 'y':
				if tilde {
					ce.key = append(ce.key, kr.ny)
				} else {
					ce.key = append(ce.key, kr.y)
				}
				tilde = false
			case 'z':
				if tilde {
					ce.key = append(ce.key, kr.nz)
				} else {
					ce.key = append(ce.key, kr.z)
				}
				tilde = false
			case 's':
				if tilde {
					ce.key = append(ce.key, kr.ns)
				} else {
					ce.key = append(ce.key, kr.s)
				}
				tilde = false
			case 'd':
				if tilde {
					ce.key = append(ce.key, kr.nd)
				} else {
					ce.key = append(ce.key, kr.d)
				}
				tilde = false
			case 'w':
				if tilde {
					ce.key = append(ce.key, kr.nw)
				} else {
					ce.key = append(ce.key, kr.w)
				}
				tilde = false
			case 'm':
				if tilde {
					ce.key = append(ce.key, kr.nm)
				} else {
					ce.key = append(ce.key, kr.m)
				}
				tilde = false
			case '$':
				switch nextChar() {
				case 'B':
					if tilde {
						ce.key = append(ce.key, CK_rBs)
					} else {
						ce.key = append(ce.key, CK_Bs)
					}
					tilde = false
				case 'D':
					if len(cestr) > 1 && cestr[1] == 'B' {
						nextChar()
						if tilde {
							ce.key = append(ce.key, CK_rDBs)
						} else {
							ce.key = append(ce.key, CK_DBs)
						}
					} else if len(cestr) > 1 && cestr[1] == 'F' {
						nextChar()
						if tilde {
							ce.key = append(ce.key, CK_rDFs)
						} else {
							ce.key = append(ce.key, CK_DFs)
						}
					} else {
						if tilde {
							ce.key = append(ce.key, CK_rDs)
						} else {
							ce.key = append(ce.key, CK_Ds)
						}
					}
					tilde = false
				case 'F':
					if tilde {
						ce.key = append(ce.key, CK_rFs)
					} else {
						ce.key = append(ce.key, CK_Fs)
					}
					tilde = false
				case 'U':
					if len(cestr) > 1 && cestr[1] == 'B' {
						nextChar()
						if tilde {
							ce.key = append(ce.key, CK_rUBs)
						} else {
							ce.key = append(ce.key, CK_UBs)
						}
					} else if len(cestr) > 1 && cestr[1] == 'F' {
						nextChar()
						if tilde {
							ce.key = append(ce.key, CK_rUFs)
						} else {
							ce.key = append(ce.key, CK_UFs)
						}
					} else {
						if tilde {
							ce.key = append(ce.key, CK_rUs)
						} else {
							ce.key = append(ce.key, CK_Us)
						}
					}
					tilde = false
				case 'L':
					if tilde {
						ce.key = append(ce.key, CK_rLs)
					} else {
						ce.key = append(ce.key, CK_Ls)
					}
					tilde = false
				case 'R':
					if tilde {
						ce.key = append(ce.key, CK_rRs)
					} else {
						ce.key = append(ce.key, CK_Rs)
					}
					tilde = false
				case 'N': // TODO: We probably don't need these but input.go currently expects them to exist (15 directions of each sign type)
					if tilde {
						ce.key = append(ce.key, CK_rNs)
					} else {
						ce.key = append(ce.key, CK_Ns)
					}
					tilde = false
				default:
					// error
					continue
				}
			case '~':
				tilde = true
			case '+':
				// do nothing
			default:
				// error
			}
			nextChar()
		}
		// Two consecutive identical directions are considered ">"
		if len(c.cmd) >= 2 && ce.IsDirection() && c.cmd[len(c.cmd)-2].IsDirection() {
			if ce.key[0] == c.cmd[len(c.cmd)-2].key[0] {
				ce.greater = true
			}
		}
	}
	if c.cmd[len(c.cmd)-1].slash {
		c.hold = append(c.hold, c.cmd[len(c.cmd)-1].key)
	}
	c.held = make([]bool, len(c.hold))
	return c, nil
}

func (c *Command) Clear(bufreset bool) {
	c.cmdidx = 0
	c.chargeidx = -1
	c.curtime = 0
	c.curkeytime = 0
	if bufreset {
		c.curbuftime = 0
	}
	for i := range c.held {
		c.held[i] = false
	}
}

// Check if inputs match the command elements
func (c *Command) bufTest(ibuf *InputBuffer, ai bool, isHelper bool, holdTemp *[CK_Last + 1]bool) bool {
	if ai && isHelper && !c.hasSlash {
		// MUGEN's internal AI can't use commands without the "/" symbol on helpers.
		return false
	}
	anyHeld, notHeld := false, 0
	if len(c.hold) > 0 && !ai {
		if holdTemp == nil {
			holdTemp = &[CK_Last + 1]bool{}
			for i := range *holdTemp {
				(*holdTemp)[i] = true
			}
		}
		allHold := true
		for i, h := range c.hold {
			func() {
				for _, k := range h {
					ks := ibuf.State(k)
					if ks == 1 && (c.cmdidx > 0 || len(c.hold) > 1) && !c.held[i] && (*holdTemp)[int(k)] {
						c.held[i], (*holdTemp)[int(k)] = true, false
					}
					if ks > 0 {
						return
					}
				}
				allHold = false
			}()
			if c.held[i] {
				anyHeld = true
			} else {
				notHeld += 1
			}
		}
		if c.cmdidx == len(c.cmd)-1 && (!allHold || notHeld > 1) {
			return anyHeld || c.cmdidx > 0
		}
	}
	if !ai && c.cmd[c.cmdidx].slash {
		if c.cmdidx > 0 {
			if notHeld == 1 {
				if len(c.cmd[c.cmdidx-1].key) != 1 {
					return false
				}
				if c.cmd[c.cmdidx-1].key[0].IsButtonPress() {
					ks := ibuf.State(c.cmd[c.cmdidx-1].key[0])
					if ks > 0 && ks <= ibuf.LastDirectionTime() {
						return true
					}
				}
			} else if len(c.cmd[c.cmdidx-1].key) > 1 {
				for _, k := range c.cmd[c.cmdidx-1].key {
					if k >= CK_a && k <= CK_m && ibuf.State(k) > 0 {
						return false
					}
				}
			}
		}
		c.cmdidx++
		c.curkeytime = 0
		return true
	}
	fail := func() bool {
		// First input requires something to be pressed/held
		if c.cmdidx == 0 {
			return anyHeld
		}
		// ">" type input check
		// There's a bug here where for instance pressing DF does not invalidate F, F. Mugen does the same thing, however
		if !ai && c.cmd[c.cmdidx].greater {
			for _, k := range c.cmd[c.cmdidx-1].key {
				if Abs(ibuf.State2(k)) == ibuf.LastChangeTime() {
					return true
				}
			}
			c.Clear(false)
			return c.bufTest(ibuf, ai, isHelper, holdTemp)
		}
		return true
	}
	if c.chargeidx != c.cmdidx {
		// If current element must be charged
		if c.cmd[c.cmdidx].chargetime > 1 {
			for _, k := range c.cmd[c.cmdidx].key {
				ks := ibuf.State(k)
				if ks > 0 {
					return ai
				}
				if func() bool {
					if ai {
						return Rand(0, c.cmd[c.cmdidx].chargetime) != 0
					}
					return -ks < c.cmd[c.cmdidx].chargetime
				}() {
					return anyHeld || c.cmdidx > 0
				}
			}
			c.chargeidx = c.cmdidx
			// Not sure what this is reproducing yet
		} else if c.cmdidx > 0 && len(c.cmd[c.cmdidx-1].key) == 1 && len(c.cmd[c.cmdidx].key) == 1 && // If elements are single key
			c.cmd[c.cmdidx-1].key[0] < CK_Us && c.cmd[c.cmdidx].key[0] < CK_rU && // "Not sign" then "not sign not release" (simple direction)
			(c.cmd[c.cmdidx-1].key[0]%15 == c.cmd[c.cmdidx].key[0]%15) { // Same direction, regardless of symbol. There are 15 directions
			if ibuf.B < 0 && ibuf.D < 0 && ibuf.F < 0 && ibuf.U < 0 { // If no direction held
				c.chargeidx = c.cmdidx
			} else {
				return fail()
			}
		}
	}
	foo := false
	for _, k := range c.cmd[c.cmdidx].key {
		n := ibuf.State2(k)
		// If "/" then buffer can be any positive number
		if c.cmd[c.cmdidx].slash {
			foo = foo || n > 0
			// If not pressed or taking too long to press all keys (?)
		} else if n < 1 || n > 7 {
			return fail()
		} else {
			foo = foo || n == 1
		}
	}
	if !foo {
		return fail()
	}
	// Conditions met. Go to next element
	c.cmdidx++
	c.curkeytime = 0
	// Both elements in a direction to button transition are checked in same the frame
	if c.cmdidx < len(c.cmd) && c.cmd[c.cmdidx-1].IsDirToButton(c.cmd[c.cmdidx]) {
		return c.bufTest(ibuf, ai, isHelper, holdTemp)
	}
	return true
}

// Update an individual command
func (c *Command) Step(ibuf *InputBuffer, ai, isHelper, hpbuf, pausebuf bool, extratime int32) {
	// Skip hitpause buffering
	if !c.buffer_hitpause {
		hpbuf = false
		extratime = 0
	}

	// Skip Pause/SuperPause buffering
	if !c.buffer_pauseend {
		pausebuf = false
		extratime = 0
	}

	// Decrease current buffer timer if not paused
	if c.curbuftime > 0 && !hpbuf && !pausebuf {
		c.curbuftime--
	}

	// Skip blank input commands
	if len(c.cmd) == 0 {
		return
	}

	// Make sure current buffer timer doesn't accidentally decrease
	ocbt := c.curbuftime
	defer func() {
		if c.curbuftime < ocbt {
			c.curbuftime = ocbt
		}
	}()

	var holdTemp *[CK_Last + 1]bool
	if ibuf == nil || !c.bufTest(ibuf, ai, isHelper, holdTemp) {
		foo := c.chargeidx == 0 && c.cmdidx == 0
		c.Clear(false)
		if foo {
			c.chargeidx = 0
		}
		return
	}

	// Handle command timers. Freeze at first input if it starts with a hold
	if c.cmdidx == 1 && c.cmd[0].slash {
		c.curtime = 0
		c.curkeytime = 0
	} else {
		c.curtime++
		c.curkeytime++
	}

	// Check if command input was completed in this frame
	c.completeframe = (c.cmdidx == len(c.cmd))

	if !c.completeframe {
		// AI ignores timers
		if ai {
			return
		}
		// Keep command going if timers allows it
		if c.curtime <= c.maxtime && (c.maxkeytime < 0 || c.curkeytime <= c.maxkeytime) {
			return
		}
	}

	// Clear command if complete or if timers expired
	c.Clear(false)

	if c.completeframe {
		// Update buffer time only if it's lower. Mugen doesn't do this but it seems like the right thing to do
		c.curbuftime = Max(c.curbuftime, c.maxbuftime+extratime)
	}
}

// Command List refers to the entire set of a character's commands
// Each player has multiple lists: one with its own commands, and a copy of each other player's lists
type CommandList struct {
	Buffer                *InputBuffer
	Names                 map[string]int
	Commands              [][]Command
	DefaultTime           int32
	DefaultKeyTime        int32
	DefaultBufferTime     int32
	DefaultBufferHitpause bool
	DefaultBufferPauseEnd bool
}

func NewCommandList(cb *InputBuffer) *CommandList {
	return &CommandList{
		Buffer:                cb,
		Names:                 make(map[string]int),
		DefaultTime:           15,
		DefaultKeyTime:        -1,
		DefaultBufferTime:     1,
		DefaultBufferHitpause: true,
		DefaultBufferPauseEnd: true,
	}
}

// Read inputs from the correct source (local, AI, net or replay) in order to update the input buffer
func (cl *CommandList) InputUpdate(controller int, facing, aiLevel float32, ibit InputBits, script bool) bool {
	if cl.Buffer == nil {
		return false
	}

	// This check is currently needed to prevent screenpack inputs from rapid firing
	// Previously it was checked outside of screenpacks as well, but that caused 1 frame delay in several places of the code
	// Such as making players wait one frame after creation to input anything or a continuous NoInput flag only resetting the buffer every two frames
	// https://github.com/ikemen-engine/Ikemen-GO/issues/1201 and https://github.com/ikemen-engine/Ikemen-GO/issues/2203
	step := true
	if script {
		step = cl.Buffer.Bb != 0
	}

	isAI := controller < 0

	var buttons [14]bool

	if isAI {
		// Since AI inputs use random numbers, we handle them locally to avoid desync
		idx := ^controller
		if idx >= 0 && idx < len(sys.aiInput) {
			sys.aiInput[idx].Update(aiLevel)
			buttons = sys.aiInput[idx].Buttons()
		}
	} else if sys.replayFile != nil {
		buttons = sys.replayFile.readReplayFile(controller)
	} else if sys.netConnection != nil {
		buttons = sys.netConnection.readNetInput(controller)
	} else {
		// If not AI, replay, or network, then it's a local human player
		if controller < len(sys.inputRemap) {
			buttons = cl.Buffer.InputReader.LocalInput(sys.inputRemap[controller], script)
		}
	}

	// Convert bool slice to named inputs
	U, D, L, R := buttons[0], buttons[1], buttons[2], buttons[3]
	a, b, c := buttons[4], buttons[5], buttons[6]
	x, y, z := buttons[7], buttons[8], buttons[9]
	s, d, w, m := buttons[10], buttons[11], buttons[12], buttons[13]

	// AssertInput flags
	// Skips button assist. Respects SOCD
	if ibit > 0 {
		U = U || ibit&IB_PU != 0
		D = D || ibit&IB_PD != 0
		L = L || ibit&IB_PL != 0
		R = R || ibit&IB_PR != 0
		a = a || ibit&IB_A != 0
		b = b || ibit&IB_B != 0
		c = c || ibit&IB_C != 0
		x = x || ibit&IB_X != 0
		y = y || ibit&IB_Y != 0
		z = z || ibit&IB_Z != 0
		s = s || ibit&IB_S != 0
		d = d || ibit&IB_D != 0
		w = w || ibit&IB_W != 0
		m = m || ibit&IB_M != 0
	}

	// Get B and F from L and R for SOCD resolution
	var B, F bool
	if facing < 0 {
		B, F = R, L
	} else {
		B, F = L, R
	}

	// Resolve SOCD for U/D and B/F
	U, D, B, F = cl.Buffer.InputReader.SocdResolution(U, D, B, F)

	// Get L and R back from B and F
	if facing < 0 {
		L, R = F, B
	} else {
		L, R = B, F
	}

	// Send final inputs to buffer
	cl.Buffer.updateInputTime(U, D, L, R, B, F, a, b, c, x, y, z, s, d, w, m)

	return step
}

// Assert commands with a given name for a given time
func (cl *CommandList) Assert(name string, time int32) bool {
	has := false
	for i := range cl.Commands {
		for j := range cl.Commands[i] {
			if cl.Commands[i][j].name == name {
				cl.Commands[i][j].curbuftime = time
				has = true
			}
		}
	}
	return has
}

// Reset command when another command with the same name is completed
// This prevents "piano inputs" from triggering the same special move with each button. TODO: This should be optional
func (cl *CommandList) ClearName(name string) {
	for i := range cl.Commands {
		for j := range cl.Commands[i] {
			if !cl.Commands[i][j].completeframe && cl.Commands[i][j].name == name {
				cl.Commands[i][j].Clear(false) // Keep their buffer time. Mugen doesn't do this but it seems like the right thing to do
			}
		}
	}
}

// Used when updating commands in each frame
func (cl *CommandList) Step(ai, isHelper, hpbuf, pausebuf bool, extratime int32) {
	if cl.Buffer != nil {
		for i := range cl.Commands {
			for j := range cl.Commands[i] {
				cl.Commands[i][j].Step(cl.Buffer, ai, isHelper, hpbuf, pausebuf, extratime)
			}
		}
		// Find completed commands and reset all duplicate instances
		// This loop must be run separately from the previous one
		for i := range cl.Commands {
			for j := range cl.Commands[i] {
				if cl.Commands[i][j].completeframe {
					cl.ClearName(cl.Commands[i][j].name)
					cl.Commands[i][j].completeframe = false
				}
			}
		}
	}
}

func (cl *CommandList) BufReset() {
	if cl.Buffer != nil {
		cl.Buffer.Reset()
	}
	for i := range cl.Commands {
		for j := range cl.Commands[i] {
			cl.Commands[i][j].Clear(true)
		}
	}
}

// Used when compiling commands
func (cl *CommandList) Add(c Command) {
	i, ok := cl.Names[c.name]
	if !ok || i < 0 || i >= len(cl.Commands) {
		i = len(cl.Commands)
		cl.Commands = append(cl.Commands, nil)
	}
	cl.Commands[i] = append(cl.Commands[i], c)
	cl.Names[c.name] = i

	generatedCmd := autoGenerateExtendedCommand(&c)
	if generatedCmd != nil {
		cl.Commands[i] = append(cl.Commands[i], *generatedCmd)
	}
}

// Used for command trigger
func (cl *CommandList) At(i int) []Command {
	if i < 0 || i >= len(cl.Commands) {
		return nil
	}
	return cl.Commands[i]
}

// Used in Lua scripts
func (cl *CommandList) Get(name string) []Command {
	i, ok := cl.Names[name]
	if !ok {
		return nil
	}
	return cl.At(i)
}

// Used in Lua scripts
func (cl *CommandList) GetState(name string) bool {
	for _, c := range cl.Get(name) {
		if c.curbuftime > 0 {
			return true
		}
	}
	return false
}

// Copy command lists from other players
// For cases where one player's inputs are compared to another's commands
func (cl *CommandList) CopyList(src CommandList) {
	cl.Names = src.Names
	cl.Commands = make([][]Command, len(src.Commands))
	for i, ca := range src.Commands {
		cl.Commands[i] = make([]Command, len(ca))
		copy(cl.Commands[i], ca)
		for j, c := range ca {
			cl.Commands[i][j].held = make([]bool, len(c.held))
		}
	}
}

func withoutTildeKey(k CommandKey) CommandKey {
	if k >= CK_rU && k <= CK_rN {
		return k - (CK_rU - CK_U)
	} else if k >= CK_Us && k <= CK_DRs {
		return k - (CK_Us - CK_U)
	} else if k >= CK_rUs && k <= CK_rNs {
		return k - (CK_rUs - CK_U)
	} else if k >= CK_ra && k <= CK_rm {
		return k - (CK_ra - CK_a)
	}
	return k
}

func autoGenerateExtendedCommand(originalCmd *Command) *Command {
	// 対象コマンドか判定
	// タメコマンド(/)や短すぎるコマンドは対象外
	if len(originalCmd.cmd) < 3 {
		return nil
	}
	for _, ce := range originalCmd.cmd {
		if ce.slash {
			return nil
		}
	}

	if len(originalCmd.cmd[0].key) == 0 {
		return nil
	}

	// 最初の方向キー入力を探す
	firstInputKey := originalCmd.cmd[0].key[0]

	var repeatPattern []cmdElem
	repeatPos := -1

	// 2番目の要素からループを開始し、最初のキーと同じキーを含む要素を探す
	for i := 1; i < len(originalCmd.cmd); i++ {
		found := false
		for _, k := range originalCmd.cmd[i].key {
			// `~` や `$` を無視して純粋なキーが同じか比較
			if withoutTildeKey(k) == withoutTildeKey(firstInputKey) {
				found = true
				break
			}
		}
		if found {
			repeatPos = i
			// 最初の入力から、それが再度現れる直前までをパターンとする
			repeatPattern = originalCmd.cmd[0:repeatPos]
			break
		}
	}

	if repeatPos == -1 {
		return nil
	}

	modifiedPattern := make([]cmdElem, len(repeatPattern))
	for i, ce := range repeatPattern {
		newKeys := make([]CommandKey, len(ce.key))
		copy(newKeys, ce.key)
		modifiedPattern[i] = ce
		modifiedPattern[i].key = newKeys
	}

	// 2番目以降のキー入力を$Nに置き換える
	if len(modifiedPattern) > 1 {
		for i := 1; i < len(modifiedPattern); i++ {
			elem := &modifiedPattern[i]
			elem.key = []CommandKey{CK_Ns}
		}
	}

	// 自動生成コマンドを作成
	newCmdSlice := make([]cmdElem, 0, len(originalCmd.cmd)+len(modifiedPattern))
	newCmdSlice = append(newCmdSlice, modifiedPattern...)
	newCmdSlice = append(newCmdSlice, originalCmd.cmd...)

	// 新規Command構造体を生成
	generatedCmd := *originalCmd
	generatedCmd.cmd = newCmdSlice
	generatedCmd.held = make([]bool, len(generatedCmd.hold))

	// 繰り返しパターンの入力数に応じて猶予フレーム数 を加算する
	timeExtension := int32(len(modifiedPattern)) * 4
	generatedCmd.maxtime += timeExtension

	return &generatedCmd
}
