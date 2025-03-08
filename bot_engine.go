package main

import (
	_ "embed"
	"fmt"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

type Event struct {
	BotID   int
	Command string
}

type Bot struct {
	ID         int
	Script     string
	Registers  map[string]int
	RegChanges chan RegChange
	ActionOut  chan Event
	RegMutex   sync.Mutex
}

// In chan any
// Out chan any
// type Msg Any
// gobits.Msg like bubbletea..
// case msg <-In:
// switch msg.(type) {
// case RegisterChangeEvent:
// ...
// }

/*
	whle true do
		motor_move_fw()
		motor_wait()
		motor_move_fw()
		local sensor = get_sensor_data()
		if sensor.obstacle_ahead then
			motor_turn_left()
			wait(1) -- 1 game tick
			say("Done!")
			shutdown()
		else
			motor_turn_right()
			wait(2) -- 2 game ticks
			say("Done!")
			shutdown()
		end
	end
*/

// will need a tick channel for handling waiting on ticks and other wait events
// (since they should only update on ticks)

/*
	accomplishment goal for "demo" -> be able to walk around a room with a robot that is scripted to follow
	you once you step within its 3x3 scan radius
*/

type RegChange struct {
	Register string
	Value    int
}

// different registers for different parts? like a motor register with just the motor parts and so on
// for specificity

func NewBot(id int, script string, out chan Event) (in chan RegChange) {
	bot := &Bot{
		ID:         id,
		Script:     script,
		Registers:  make(map[string]int),
		RegChanges: make(chan RegChange),
		ActionOut:  out,
	}
	go bot.eventHandler()
	go bot.runLuaScript()
	return bot.RegChanges
}

func (bot *Bot) eventHandler() {
	for {
		event := <-bot.RegChanges
		bot.RegMutex.Lock()
		bot.Registers[event.Register] = event.Value
		bot.RegMutex.Unlock()
	}
}

func (bot *Bot) runLuaScript() {
	L := lua.NewState()
	defer L.Close()

	registerBotFuncs(L, bot)

	if err := L.DoString(bot.Script); err != nil {
		panic(err)
	}
}

func registerBotFuncs(L *lua.LState, bot *Bot) {
	L.SetGlobal("motor_wait", L.NewFunction(func(L *lua.LState) int {
		for {
			bot.RegMutex.Lock()
			ready := bot.Registers["motor_ready"]
			bot.RegMutex.Unlock()
			if ready == 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		return 0
	}))

	L.SetGlobal("motor_step_fw", L.NewFunction(func(L *lua.LState) int {
		bot.RegMutex.Lock()
		defer bot.RegMutex.Unlock()
		if bot.Registers["motor_ready"] == 0 {
			bot.Registers["motor_ready"] = 1
			bot.ActionOut <- Event{BotID: bot.ID, Command: "move_fw"}
		}
		return 0
	}))
}

//go:embed test_scripts/walk_forward.lua
var walk_script string

type MapData struct {
	Tiles [][]string
}

type BotData struct {
	PosX int
	PosY int
	In   chan RegChange
}

type DelayedAction struct {
	TargetTick int
	Action     func()
}

func main() {
	numBots := 2
	bots := make(map[int]BotData)
	eventQueue := make(chan Event, 8)
	for i := range numBots {
		botIn := NewBot(i, walk_script, eventQueue)
		bots[i] = BotData{0, 0, botIn}
	}

	messageQueue := make(chan string)

	fmt.Println("starting")
	go func(out chan string) {
		var tickCount int
		var delayedActions []DelayedAction

		toProcess := make(chan Event, 8)
		timer := time.NewTimer(500 * time.Millisecond)

		for {
			select {
			case <-timer.C:
				out <- "game tick"

			drainLoop:
				for {
					select {
					case event := <-toProcess:
						if event.Command == "move_fw" {
							out <- fmt.Sprintf("bot %d has moved forward", event.BotID)
							delayedActions = append(delayedActions, DelayedAction{
								TargetTick: tickCount + 1,
								Action: func() {
									bots[event.BotID].In <- RegChange{"motor_ready", 0}
								},
							})
						}
					default:
						// no more events
						break drainLoop
					}
				}

				// execute delayed actions
				var remaining []DelayedAction
				for _, da := range delayedActions {
					if da.TargetTick == tickCount {
						da.Action()
					} else {
						remaining = append(remaining, da)
					}
				}
				delayedActions = remaining

				out <- "done processing"
				tickCount++
				timer.Reset(500 * time.Millisecond)

			case event := <-eventQueue:
				toProcess <- event
			}
		}
	}(messageQueue)

	for msg := range messageQueue {
		fmt.Println(msg)
	}
}
