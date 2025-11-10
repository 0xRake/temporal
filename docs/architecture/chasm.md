# CHASM (Coordinated Heterogenous Application State Machine)

Table of Contents

* [Overview](#overview)
    * [Motivation](#motivation)
    * [HSM Learnings](#hsm-learnings)
    * [CHASM Differences](#chasm-differences)
* [Writing an Entity](#writing-an-entity)
* [Testing an Entity](#testing-an-entity)
* [CHASM Details](#chasm-details)
    * [Persisting Changes](#how-chasm-persists-changes)
    * [Generating Tasks](#how-chasm-generates-tasks)

## Overview

CHASM (Coordinated Heterogeneous Application State Machine) is Temporal's next-generation state machine framework for modeling complex workflow components and their interactions. CHASM organizes components as a tree structure where each component has its own lifecycle, state, and can generate tasks. It replaces the HSM (Hierarchical State Machine) framework with a more flexible and composable design.

### Motivation

Temporal workflows need to manage complex hierarchies of sub-entities such as:
- Callbacks with retry logic and timeouts
- Activities with their own lifecycle states
- Timers and scheduled events
- Nexus operations that span multiple services

These entities need to:
1. **Maintain their own state** independently while coordinating with the parent workflow
2. **Generate tasks** for future execution (invocations, retries, timeouts)
3. **Update search attributes** to make workflow state searchable
4. **Reference each other** for coordination (e.g., a callback pointing to its completion source)
5. **Persist efficiently** without loading the entire workflow state
6. **Support replication** across clusters with conflict detection

The challenge was creating a framework that supports all these requirements while keeping component implementations simple and testable.

### HSM Learnings

The previous HSM (Hierarchical State Machine) framework taught us several lessons:

**What Worked**:
- State machine pattern with explicit transitions
- Task generation tied to state changes
- Separation of state from behavior

**What Didn't Work**:
1. **Flat Collections**: HSM stored components in flat collections within a single tree node, making deep nesting awkward
2. **Homogeneous Components**: Collections could only hold components of the same type
3. **Task Regeneration**: Tasks were regenerated on every replication, which was inefficient
4. **Limited Composition**: Hard to model components that reference other components
5. **Tight Coupling**: HSM components were tightly coupled to mutable state implementation

### CHASM Differences

CHASM addresses HSM's limitations with these key differences:

| Aspect | HSM | CHASM |
|--------|-----|-------|
| **Structure** | Flat collections | Tree hierarchy |
| **Components** | Homogeneous (same type) | Heterogeneous (mixed types) |
| **State** | `State()` and `SetState()` | `LifecycleState()` + component fields |
| **Tasks** | Regenerated from state | Persisted per component |
| **Persistence** | Single blob | Separate row per node |
| **References** | Not supported | Pointer fields |
| **Transactions** | Direct mutation | Context-based with deferred execution |

**Core CHASM Principles**:
1. **Tree-Based Hierarchy**: Components form a tree where each node is independently persisted
2. **Lifecycle Management**: Every component has a lifecycle state (Running, Completed, Failed)
3. **Task Ownership**: Tasks are owned by the component that generates them
4. **Context-Based Updates**: All mutations happen through `MutableContext` within transactions
5. **Lazy Loading**: Components are deserialized on-demand (future optimization)
6. **Type Safety**: Strong typing with generics for components and fields

## Writing an Entity

This section walks through creating a CHASM component using a simplified, real-world example: **a Coffee Machine**. This example demonstrates all key CHASM concepts without requiring Temporal-specific knowledge.

### Example: Coffee Machine State Machine

We'll build a coffee machine that:
- Grinds beans, brews coffee, and dispenses drinks
- Tracks water and bean levels with nested components
- Handles failures with retry logic
- Auto-cleans after periods of inactivity
- Generates tasks for hardware operations

**State Flow:**
```
Idle → Grinding → Brewing → Dispensing → Idle
                    ↓ (timeout/failure)
                 Cleaning
```

### Step 1: Define the State Proto

First, define your states and component data in protobuf:

```protobuf
// coffee_machine.proto
syntax = "proto3";

package coffeemachine.v1;

enum MachineState {
    MACHINE_STATE_IDLE = 0;
    MACHINE_STATE_GRINDING = 1;
    MACHINE_STATE_BREWING = 2;
    MACHINE_STATE_DISPENSING = 3;
    MACHINE_STATE_CLEANING = 4;
    MACHINE_STATE_FAILED = 5;
}

message CoffeeMachineData {
    MachineState state = 1;
    int32 cups_made = 2;
    google.protobuf.Timestamp last_clean_time = 3;
    int32 brew_attempt = 4;
}

message WaterTankData {
    int32 level_ml = 1;  // Current water level in milliliters
    int32 capacity_ml = 2;
}

message BeanHopperData {
    int32 level_grams = 1;  // Current bean level in grams
    int32 capacity_grams = 2;
}

// Task definitions
message GrindBeansTask {
    int32 grams = 1;
}

message BrewCoffeeTask {
    int32 water_ml = 1;
    int32 temperature_celsius = 2;
}

message DispenseCoffeeTask {
    int32 volume_ml = 1;
}

message CleanMachineTask {
    bool deep_clean = 1;
}

message CheckSuppliesTask {}  // Pure task - just checks state
```

### Step 2: Define Component Structures

```go
package coffeemachine

import (
    "go.temporal.io/server/chasm"
    coffeepb "your/path/to/coffeemachine/v1"
)

const (
    CoffeeMachineArchetype = "CoffeeMachine"
    WaterTankArchetype     = "WaterTank"
    BeanHopperArchetype    = "BeanHopper"
)

// Main coffee machine component
type CoffeeMachine struct {
    chasm.UnimplementedComponent

    // Persisted state
    *coffeepb.CoffeeMachineData

    // Nested components
    WaterTank  chasm.Field[*WaterTank]
    BeanHopper chasm.Field[*BeanHopper]
}

func (cm *CoffeeMachine) LifecycleState(_ chasm.Context) chasm.LifecycleState {
    switch cm.State {
    case coffeepb.MACHINE_STATE_FAILED:
        return chasm.LifecycleStateFailed
    default:
        return chasm.LifecycleStateRunning
    }
}

// StateMachine interface for transitions
func (cm *CoffeeMachine) StateMachineState() coffeepb.MachineState {
    return cm.State
}

func (cm *CoffeeMachine) SetStateMachineState(state coffeepb.MachineState) {
    cm.State = state
}

// Nested component: Water Tank
type WaterTank struct {
    chasm.UnimplementedComponent
    *coffeepb.WaterTankData
}

func (wt *WaterTank) LifecycleState(_ chasm.Context) chasm.LifecycleState {
    // Always running - tanks don't complete
    return chasm.LifecycleStateRunning
}

// Nested component: Bean Hopper
type BeanHopper struct {
    chasm.UnimplementedComponent
    *coffeepb.BeanHopperData
}

func (bh *BeanHopper) LifecycleState(_ chasm.Context) chasm.LifecycleState {
    return chasm.LifecycleStateRunning
}
```

### Step 3: Define Events and Transitions

```go
// Events that trigger state transitions
type EventStartBrewing struct {
    CoffeeType string
    SizeML     int32
}

type EventBrewingComplete struct {
    Time time.Time
}

type EventBrewingFailed struct {
    Reason string
    Time   time.Time
}

type EventDispensingComplete struct{}

type EventStartCleaning struct {
    DeepClean bool
}

// Transition: Idle → Grinding
var TransitionStartGrinding = chasm.NewTransition(
    []coffeepb.MachineState{coffeepb.MACHINE_STATE_IDLE},
    coffeepb.MACHINE_STATE_GRINDING,
    func(cm *CoffeeMachine, ctx chasm.MutableContext, event EventStartBrewing) error {
        // Check if we have enough supplies
        waterTank, err := cm.WaterTank.Get(ctx)
        if err != nil {
            return err
        }
        beanHopper, err := cm.BeanHopper.Get(ctx)
        if err != nil {
            return err
        }

        if waterTank.LevelMl < event.SizeML {
            return fmt.Errorf("insufficient water: need %dml, have %dml",
                event.SizeML, waterTank.LevelMl)
        }

        gramsNeeded := event.SizeML / 10 // 1 gram per 10ml
        if beanHopper.LevelGrams < gramsNeeded {
            return fmt.Errorf("insufficient beans: need %dg, have %dg",
                gramsNeeded, beanHopper.LevelGrams)
        }

        // Generate grinding task (side-effect)
        ctx.AddTask(cm, chasm.TaskAttributes{}, &coffeepb.GrindBeansTask{
            Grams: gramsNeeded,
        })

        return nil
    },
)

// Transition: Grinding → Brewing
var TransitionStartBrewing = chasm.NewTransition(
    []coffeepb.MachineState{coffeepb.MACHINE_STATE_GRINDING},
    coffeepb.MACHINE_STATE_BREWING,
    func(cm *CoffeeMachine, ctx chasm.MutableContext, event EventBrewingComplete) error {
        // Update bean level
        beanHopper, err := cm.BeanHopper.Get(ctx)
        if err != nil {
            return err
        }
        beanHopper.LevelGrams -= 20  // Used 20 grams

        // Generate brewing task (side-effect)
        ctx.AddTask(cm, chasm.TaskAttributes{}, &coffeepb.BrewCoffeeTask{
            WaterMl:            200,
            TemperatureCelsius: 93,
        })

        return nil
    },
)

// Transition: Brewing → Dispensing
var TransitionStartDispensing = chasm.NewTransition(
    []coffeepb.MachineState{coffeepb.MACHINE_STATE_BREWING},
    coffeepb.MACHINE_STATE_DISPENSING,
    func(cm *CoffeeMachine, ctx chasm.MutableContext, event EventBrewingComplete) error {
        // Update water level
        waterTank, err := cm.WaterTank.Get(ctx)
        if err != nil {
            return err
        }
        waterTank.LevelMl -= 200  // Used 200ml

        // Generate dispensing task (side-effect)
        ctx.AddTask(cm, chasm.TaskAttributes{}, &coffeepb.DispenseCoffeeTask{
            VolumeMl: 200,
        })

        return nil
    },
)

// Transition: Dispensing → Idle
var TransitionComplete = chasm.NewTransition(
    []coffeepb.MachineState{coffeepb.MACHINE_STATE_DISPENSING},
    coffeepb.MACHINE_STATE_IDLE,
    func(cm *CoffeeMachine, ctx chasm.MutableContext, event EventDispensingComplete) error {
        cm.CupsMade++

        // Schedule auto-clean check (pure task) for 2 hours later
        if cm.CupsMade%10 == 0 {
            ctx.AddTask(cm, chasm.TaskAttributes{
                ScheduledTime: ctx.Now(cm).Add(2 * time.Hour),
            }, &coffeepb.CheckSuppliesTask{})
        }

        return nil
    },
)

// Transition: Any → Cleaning (on failure or timeout)
var TransitionStartCleaning = chasm.NewTransition(
    []coffeepb.MachineState{
        coffeepb.MACHINE_STATE_IDLE,
        coffeepb.MACHINE_STATE_BREWING,
    },
    coffeepb.MACHINE_STATE_CLEANING,
    func(cm *CoffeeMachine, ctx chasm.MutableContext, event EventStartCleaning) error {
        // Generate cleaning task
        ctx.AddTask(cm, chasm.TaskAttributes{}, &coffeepb.CleanMachineTask{
            DeepClean: event.DeepClean,
        })

        cm.LastCleanTime = timestamppb.New(ctx.Now(cm))

        return nil
    },
)

// Transition: Brewing → Idle (on failure with retry)
var TransitionBrewingFailed = chasm.NewTransition(
    []coffeepb.MachineState{coffeepb.MACHINE_STATE_BREWING},
    coffeepb.MACHINE_STATE_IDLE,
    func(cm *CoffeeMachine, ctx chasm.MutableContext, event EventBrewingFailed) error {
        cm.BrewAttempt++

        if cm.BrewAttempt >= 3 {
            // Too many failures, transition to FAILED state
            cm.State = coffeepb.MACHINE_STATE_FAILED
            return fmt.Errorf("brewing failed after 3 attempts: %s", event.Reason)
        }

        // Schedule retry after backoff
        retryDelay := time.Duration(cm.BrewAttempt) * 30 * time.Second

        // This would trigger a new brew attempt (simplified)
        ctx.AddTask(cm, chasm.TaskAttributes{
            ScheduledTime: event.Time.Add(retryDelay),
        }, &coffeepb.CheckSuppliesTask{})

        return nil
    },
)
```

### Step 4: Implement Task Executors

```go
// Side-effect task: Actually controls hardware
type GrindBeansExecutor struct {
    grinderController HardwareController
}

func (e *GrindBeansExecutor) Execute(
    ctx context.Context,
    ref chasm.ComponentRef,
    attrs chasm.TaskAttributes,
    task *coffeepb.GrindBeansTask,
) error {
    // Control actual hardware
    fmt.Printf("Grinding %d grams of beans...\n", task.Grams)

    err := e.grinderController.Grind(ctx, task.Grams)
    if err != nil {
        // Update machine state on failure
        _, _, err = chasm.UpdateComponent(
            chasm.NewEngineContext(ctx, e.engine),
            ref,
            func(cm *CoffeeMachine, ctx chasm.MutableContext, _ struct{}) (struct{}, error) {
                return struct{}{}, TransitionBrewingFailed.Apply(ctx, cm, EventBrewingFailed{
                    Reason: err.Error(),
                    Time:   time.Now(),
                })
            },
            struct{}{},
        )
        return err
    }

    // Success - move to next state
    _, _, err = chasm.UpdateComponent(
        chasm.NewEngineContext(ctx, e.engine),
        ref,
        func(cm *CoffeeMachine, ctx chasm.MutableContext, _ struct{}) (struct{}, error) {
            return struct{}{}, TransitionStartBrewing.Apply(ctx, cm, EventBrewingComplete{
                Time: time.Now(),
            })
        },
        struct{}{},
    )

    return err
}

func (e *GrindBeansExecutor) Validate(
    ctx chasm.Context,
    cm *CoffeeMachine,
    attrs chasm.TaskAttributes,
    task *coffeepb.GrindBeansTask,
) (bool, error) {
    // Only execute if still in GRINDING state
    return cm.State == coffeepb.MACHINE_STATE_GRINDING, nil
}

// Pure task: Only updates CHASM state
type CheckSuppliesExecutor struct{}

func (e *CheckSuppliesExecutor) Execute(
    ctx chasm.MutableContext,
    cm *CoffeeMachine,
    attrs chasm.TaskAttributes,
    task *coffeepb.CheckSuppliesTask,
) error {
    // Check water level
    waterTank, err := cm.WaterTank.Get(ctx)
    if err != nil {
        return err
    }

    // Check bean level
    beanHopper, err := cm.BeanHopper.Get(ctx)
    if err != nil {
        return err
    }

    // Trigger cleaning if needed (state-only operation)
    timeSinceClean := ctx.Now(cm).Sub(cm.LastCleanTime.AsTime())
    if timeSinceClean > 24*time.Hour || cm.CupsMade > 50 {
        return TransitionStartCleaning.Apply(ctx, cm, EventStartCleaning{
            DeepClean: timeSinceClean > 7*24*time.Hour,
        })
    }

    // Log if supplies are low (no side effects)
    if waterTank.LevelMl < 500 || beanHopper.LevelGrams < 100 {
        fmt.Printf("Warning: Low supplies - Water: %dml, Beans: %dg\n",
            waterTank.LevelMl, beanHopper.LevelGrams)
    }

    return nil
}

func (e *CheckSuppliesExecutor) Validate(
    ctx chasm.Context,
    cm *CoffeeMachine,
    attrs chasm.TaskAttributes,
    task *coffeepb.CheckSuppliesTask,
) (bool, error) {
    // Always valid
    return true, nil
}
```

### Step 5: Create and Use the Coffee Machine

```go
// Create a new coffee machine entity
func NewCoffeeMachineHandler(
    ctx chasm.EngineContext,
    req NewCoffeeMachineRequest,
) (NewCoffeeMachineResponse, error) {
    output, entityKey, ref, err := chasm.NewEntity(
        ctx,
        chasm.EntityKey{
            NamespaceID: req.NamespaceID,
            WorkflowID:  req.MachineID,  // Machine serial number
            RunID:       uuid.NewString(),
        },
        func(ctx chasm.MutableContext, input NewCoffeeMachineRequest) (
            *CoffeeMachine, NewCoffeeMachineResponse, error,
        ) {
            // Create nested components
            waterTank := &WaterTank{
                WaterTankData: &coffeepb.WaterTankData{
                    LevelMl:     3000,
                    CapacityMl:  3000,
                },
            }

            beanHopper := &BeanHopper{
                BeanHopperData: &coffeepb.BeanHopperData{
                    LevelGrams:     500,
                    CapacityGrams:  500,
                },
            }

            // Create main machine component
            machine := &CoffeeMachine{
                CoffeeMachineData: &coffeepb.CoffeeMachineData{
                    State:         coffeepb.MACHINE_STATE_IDLE,
                    CupsMade:      0,
                    LastCleanTime: timestamppb.New(ctx.Now(waterTank)),
                },
                WaterTank:  chasm.NewComponentField(ctx, waterTank),
                BeanHopper: chasm.NewComponentField(ctx, beanHopper),
            }

            return machine, NewCoffeeMachineResponse{
                MachineID: entityKey.WorkflowID,
                Status:    "ready",
            }, nil
        },
        req,
    )

    return output, err
}

// Make coffee
func MakeCoffeeHandler(
    ctx chasm.EngineContext,
    req MakeCoffeeRequest,
) (MakeCoffeeResponse, error) {
    output, ref, err := chasm.UpdateComponent(
        ctx,
        req.MachineRef,
        func(cm *CoffeeMachine, ctx chasm.MutableContext, input MakeCoffeeRequest) (
            MakeCoffeeResponse, error,
        ) {
            // Start the brewing process
            err := TransitionStartGrinding.Apply(ctx, cm, EventStartBrewing{
                CoffeeType: input.CoffeeType,
                SizeML:     input.SizeML,
            })
            if err != nil {
                return MakeCoffeeResponse{Success: false, Error: err.Error()}, err
            }

            return MakeCoffeeResponse{
                Success: true,
                EstimatedTime: 90 * time.Second,
            }, nil
        },
        req,
    )

    return output, err
}
```

### Step 6: Register Library

```go
package coffeemachine

const LibraryName chasm.LibraryName = "coffeemachine"

type Library struct {
    GrindBeansExecutor     *GrindBeansExecutor
    BrewCoffeeExecutor     *BrewCoffeeExecutor
    DispenseExecutor       *DispenseExecutor
    CleanExecutor          *CleanExecutor
    CheckSuppliesExecutor  *CheckSuppliesExecutor
}

func (l *Library) Name() chasm.LibraryName {
    return LibraryName
}

func (l *Library) Components() []*chasm.RegistrableComponent {
    return []*chasm.RegistrableComponent{
        chasm.NewRegistrableComponent(
            CoffeeMachineArchetype,
            func() *CoffeeMachine { return &CoffeeMachine{} },
        ),
        chasm.NewRegistrableComponent(
            WaterTankArchetype,
            func() *WaterTank { return &WaterTank{} },
        ),
        chasm.NewRegistrableComponent(
            BeanHopperArchetype,
            func() *BeanHopper { return &BeanHopper{} },
        ),
    }
}

func (l *Library) Tasks() []*chasm.RegistrableTask {
    return []*chasm.RegistrableTask{
        // Side-effect tasks
        chasm.NewRegistrableSideEffectTask("grind", l.GrindBeansExecutor, l.GrindBeansExecutor),
        chasm.NewRegistrableSideEffectTask("brew", l.BrewCoffeeExecutor, l.BrewCoffeeExecutor),
        chasm.NewRegistrableSideEffectTask("dispense", l.DispenseExecutor, l.DispenseExecutor),
        chasm.NewRegistrableSideEffectTask("clean", l.CleanExecutor, l.CleanExecutor),

        // Pure task
        chasm.NewRegistrablePureTask("check_supplies", l.CheckSuppliesExecutor, l.CheckSuppliesExecutor),
    }
}
```

### Key Concepts Demonstrated

This coffee machine example shows:

1. **Component Hierarchy**: `CoffeeMachine` contains `WaterTank` and `BeanHopper` as nested components
2. **Lifecycle States**: Machine can be Running or Failed; nested components always Running
3. **State Machine Transitions**: Explicit transitions between states (Idle → Grinding → Brewing → Dispensing)
4. **Event-Driven**: Transitions triggered by events (`EventStartBrewing`, `EventBrewingComplete`)
5. **Task Generation**:
   - Side-effect tasks control hardware (grinding, brewing, dispensing)
   - Pure tasks update state only (checking supplies)
6. **Retry Logic**: Failed brewing attempts trigger retries with backoff
7. **Scheduled Tasks**: Auto-clean check scheduled 2 hours after every 10th cup
8. **State Validation**: Task validators ensure tasks only execute in appropriate states
9. **Nested Component Access**: Transitions can read/modify nested components (water/bean levels)

This pattern applies to any state machine - replace "coffee machine" with "workflow", "callback", or any other entity!

## Testing an Entity

Testing CHASM components involves three levels: unit tests for transitions, integration tests for the component tree, and functional tests for end-to-end flows. Here's how to test the Coffee Machine example.

### Unit Testing Transitions

Test individual state transitions in isolation:

```go
package coffeemachine

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    "go.temporal.io/server/chasm"
)

// Mock context for unit tests
type mockMutableContext struct {
    chasm.MutableContext
    tasks []taskWithComponent
    now   time.Time
}

type taskWithComponent struct {
    component interface{}
    attrs     chasm.TaskAttributes
    task      interface{}
}

func (m *mockMutableContext) AddTask(component chasm.Component, attrs chasm.TaskAttributes, task interface{}) {
    m.tasks = append(m.tasks, taskWithComponent{
        component: component,
        attrs:     attrs,
        task:      task,
    })
}

func (m *mockMutableContext) Now(component chasm.Component) time.Time {
    return m.now
}

func TestTransitionStartGrinding_Success(t *testing.T) {
    // Setup
    machine := &CoffeeMachine{
        CoffeeMachineData: &coffeepb.CoffeeMachineData{
            State:    coffeepb.MACHINE_STATE_IDLE,
            CupsMade: 0,
        },
        WaterTank: createMockField(&WaterTank{
            WaterTankData: &coffeepb.WaterTankData{
                LevelMl:    3000,
                CapacityMl: 3000,
            },
        }),
        BeanHopper: createMockField(&BeanHopper{
            BeanHopperData: &coffeepb.BeanHopperData{
                LevelGrams:     500,
                CapacityGrams:  500,
            },
        }),
    }

    mockCtx := &mockMutableContext{
        now:   time.Now(),
        tasks: []taskWithComponent{},
    }

    // Execute transition
    err := TransitionStartGrinding.Apply(mockCtx, machine, EventStartBrewing{
        CoffeeType: "espresso",
        SizeML:     200,
    })

    // Verify
    require.NoError(t, err)
    require.Equal(t, coffeepb.MACHINE_STATE_GRINDING, machine.State)
    require.Len(t, mockCtx.tasks, 1)

    // Verify task
    grindTask, ok := mockCtx.tasks[0].task.(*coffeepb.GrindBeansTask)
    require.True(t, ok)
    require.Equal(t, int32(20), grindTask.Grams)
}

func TestTransitionStartGrinding_InsufficientWater(t *testing.T) {
    machine := &CoffeeMachine{
        CoffeeMachineData: &coffeepb.CoffeeMachineData{
            State: coffeepb.MACHINE_STATE_IDLE,
        },
        WaterTank: createMockField(&WaterTank{
            WaterTankData: &coffeepb.WaterTankData{
                LevelMl:    50,  // Not enough
                CapacityMl: 3000,
            },
        }),
        BeanHopper: createMockField(&BeanHopper{
            BeanHopperData: &coffeepb.BeanHopperData{
                LevelGrams:     500,
                CapacityGrams:  500,
            },
        }),
    }

    mockCtx := &mockMutableContext{}

    // Execute transition
    err := TransitionStartGrinding.Apply(mockCtx, machine, EventStartBrewing{
        CoffeeType: "espresso",
        SizeML:     200,
    })

    // Verify error
    require.Error(t, err)
    require.Contains(t, err.Error(), "insufficient water")
    require.Equal(t, coffeepb.MACHINE_STATE_IDLE, machine.State)
    require.Len(t, mockCtx.tasks, 0)
}

func TestTransitionComplete_SchedulesCleanCheck(t *testing.T) {
    machine := &CoffeeMachine{
        CoffeeMachineData: &coffeepb.CoffeeMachineData{
            State:    coffeepb.MACHINE_STATE_DISPENSING,
            CupsMade: 9,  // Next cup will be 10th
        },
    }

    now := time.Now()
    mockCtx := &mockMutableContext{
        now:   now,
        tasks: []taskWithComponent{},
    }

    // Execute transition
    err := TransitionComplete.Apply(mockCtx, machine, EventDispensingComplete{})

    // Verify
    require.NoError(t, err)
    require.Equal(t, coffeepb.MACHINE_STATE_IDLE, machine.State)
    require.Equal(t, int32(10), machine.CupsMade)

    // Verify scheduled task
    require.Len(t, mockCtx.tasks, 1)
    require.Equal(t, now.Add(2*time.Hour), mockCtx.tasks[0].attrs.ScheduledTime)

    checkTask, ok := mockCtx.tasks[0].task.(*coffeepb.CheckSuppliesTask)
    require.True(t, ok)
    require.NotNil(t, checkTask)
}
```

### Integration Testing with Component Tree

Test components with the CHASM tree framework:

```go
package coffeemachine

import (
    "testing"

    "github.com/golang/mock/gomock"
    "github.com/stretchr/testify/suite"
    "go.temporal.io/server/chasm"
    "go.temporal.io/server/common/clock"
    "go.temporal.io/server/common/log"
)

type CoffeeMachineSuite struct {
    suite.Suite

    controller      *gomock.Controller
    registry        *chasm.Registry
    timeSource      *clock.EventTimeSource
    logger          log.Logger
}

func TestCoffeeMachineSuite(t *testing.T) {
    suite.Run(t, new(CoffeeMachineSuite))
}

func (s *CoffeeMachineSuite) SetupTest() {
    s.controller = gomock.NewController(s.T())
    s.registry = chasm.NewRegistry(s.logger)
    s.timeSource = clock.NewEventTimeSource()

    // Register coffee machine library
    library := NewLibrary()
    err := s.registry.Register(library)
    s.NoError(err)
}

func (s *CoffeeMachineSuite) TestCreateMachine() {
    // Create new entity
    output, entityKey, ref, err := chasm.NewEntity(
        s.createContext(),
        chasm.EntityKey{
            NamespaceID: "test-namespace",
            WorkflowID:  "machine-001",
            RunID:       "run-001",
        },
        func(ctx chasm.MutableContext, input struct{}) (*CoffeeMachine, struct{}, error) {
            waterTank := &WaterTank{
                WaterTankData: &coffeepb.WaterTankData{
                    LevelMl:    3000,
                    CapacityMl: 3000,
                },
            }

            beanHopper := &BeanHopper{
                BeanHopperData: &coffeepb.BeanHopperData{
                    LevelGrams:     500,
                    CapacityGrams:  500,
                },
            }

            machine := &CoffeeMachine{
                CoffeeMachineData: &coffeepb.CoffeeMachineData{
                    State:         coffeepb.MACHINE_STATE_IDLE,
                    CupsMade:      0,
                    LastCleanTime: timestamppb.New(ctx.Now(waterTank)),
                },
                WaterTank:  chasm.NewComponentField(ctx, waterTank),
                BeanHopper: chasm.NewComponentField(ctx, beanHopper),
            }

            return machine, struct{}{}, nil
        },
        struct{}{},
    )

    // Verify
    s.NoError(err)
    s.NotNil(ref)
    s.Equal("machine-001", entityKey.WorkflowID)
}

func (s *CoffeeMachineSuite) TestMakeCoffee_FullFlow() {
    // Create machine
    _, _, ref, err := s.createMachine()
    s.NoError(err)

    // Start brewing
    _, _, err = chasm.UpdateComponent(
        s.createContext(),
        ref,
        func(cm *CoffeeMachine, ctx chasm.MutableContext, _ struct{}) (struct{}, error) {
            return struct{}{}, TransitionStartGrinding.Apply(ctx, cm, EventStartBrewing{
                CoffeeType: "espresso",
                SizeML:     200,
            })
        },
        struct{}{},
    )
    s.NoError(err)

    // Verify state changed
    _, err = chasm.ReadComponent(
        s.createContext(),
        ref,
        func(ctx chasm.Context, cm *CoffeeMachine, _ struct{}) (struct{}, error) {
            s.Equal(coffeepb.MACHINE_STATE_GRINDING, cm.State)
            return struct{}{}, nil
        },
        struct{}{},
    )
    s.NoError(err)
}

func (s *CoffeeMachineSuite) TestNestedComponentAccess() {
    _, _, ref, err := s.createMachine()
    s.NoError(err)

    // Read nested components
    _, err = chasm.ReadComponent(
        s.createContext(),
        ref,
        func(ctx chasm.Context, cm *CoffeeMachine, _ struct{}) (struct{}, error) {
            // Access water tank
            waterTank, err := cm.WaterTank.Get(ctx)
            s.NoError(err)
            s.Equal(int32(3000), waterTank.LevelMl)

            // Access bean hopper
            beanHopper, err := cm.BeanHopper.Get(ctx)
            s.NoError(err)
            s.Equal(int32(500), beanHopper.LevelGrams)

            return struct{}{}, nil
        },
        struct{}{},
    )
    s.NoError(err)
}

func (s *CoffeeMachineSuite) createMachine() (struct{}, chasm.EntityKey, chasm.ComponentRef, error) {
    return chasm.NewEntity(
        s.createContext(),
        chasm.EntityKey{
            NamespaceID: "test-namespace",
            WorkflowID:  "machine-test",
            RunID:       "run-test",
        },
        func(ctx chasm.MutableContext, _ struct{}) (*CoffeeMachine, struct{}, error) {
            // ... create machine (same as TestCreateMachine)
        },
        struct{}{},
    )
}
```

### Testing Task Executors

Test side-effect and pure task executors:

```go
func TestGrindBeansExecutor_Success(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    mockGrinder := NewMockHardwareController(ctrl)
    executor := &GrindBeansExecutor{
        grinderController: mockGrinder,
    }

    // Expect hardware call
    mockGrinder.EXPECT().
        Grind(gomock.Any(), int32(20)).
        Return(nil)

    // Execute task
    err := executor.Execute(
        context.Background(),
        testComponentRef,
        chasm.TaskAttributes{},
        &coffeepb.GrindBeansTask{Grams: 20},
    )

    require.NoError(t, err)
}

func TestGrindBeansExecutor_HardwareFailure(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    mockGrinder := NewMockHardwareController(ctrl)
    executor := &GrindBeansExecutor{
        grinderController: mockGrinder,
    }

    // Simulate hardware failure
    mockGrinder.EXPECT().
        Grind(gomock.Any(), int32(20)).
        Return(fmt.Errorf("grinder jam"))

    err := executor.Execute(
        context.Background(),
        testComponentRef,
        chasm.TaskAttributes{},
        &coffeepb.GrindBeansTask{Grams: 20},
    )

    require.Error(t, err)
}

func TestCheckSuppliesExecutor_TriggersClean(t *testing.T) {
    machine := &CoffeeMachine{
        CoffeeMachineData: &coffeepb.CoffeeMachineData{
            State:         coffeepb.MACHINE_STATE_IDLE,
            CupsMade:      60,  // Over threshold
            LastCleanTime: timestamppb.New(time.Now().Add(-25 * time.Hour)),
        },
    }

    mockCtx := &mockMutableContext{
        now:   time.Now(),
        tasks: []taskWithComponent{},
    }

    executor := &CheckSuppliesExecutor{}
    err := executor.Execute(
        mockCtx,
        machine,
        chasm.TaskAttributes{},
        &coffeepb.CheckSuppliesTask{},
    )

    // Should transition to cleaning
    require.NoError(t, err)
    require.Equal(t, coffeepb.MACHINE_STATE_CLEANING, machine.State)
}

func TestTaskValidator_StateCheck(t *testing.T) {
    executor := &GrindBeansExecutor{}

    tests := []struct {
        name     string
        state    coffeepb.MachineState
        expected bool
    }{
        {"grinding state - valid", coffeepb.MACHINE_STATE_GRINDING, true},
        {"idle state - invalid", coffeepb.MACHINE_STATE_IDLE, false},
        {"brewing state - invalid", coffeepb.MACHINE_STATE_BREWING, false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            machine := &CoffeeMachine{
                CoffeeMachineData: &coffeepb.CoffeeMachineData{
                    State: tt.state,
                },
            }

            valid, err := executor.Validate(
                nil,
                machine,
                chasm.TaskAttributes{},
                &coffeepb.GrindBeansTask{},
            )

            require.NoError(t, err)
            require.Equal(t, tt.expected, valid)
        })
    }
}
```

### Functional Testing (End-to-End)

Test complete scenarios with actual CHASM engine (see `tests/chasm_test.go:23`):

```go
package tests

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/suite"
    "go.temporal.io/server/chasm"
    "go.temporal.io/server/tests/testcore"
)

type CoffeeMachineFunctionalSuite struct {
    testcore.FunctionalTestBase
    chasmEngine chasm.Engine
}

func TestCoffeeMachineFunctional(t *testing.T) {
    suite.Run(t, new(CoffeeMachineFunctionalSuite))
}

func (s *CoffeeMachineFunctionalSuite) SetupSuite() {
    s.FunctionalTestBase.SetupSuiteWithCluster(
        testcore.WithDynamicConfigOverrides(map[dynamicconfig.Key]any{
            dynamicconfig.EnableChasm.Key(): true,
        }),
    )

    var err error
    s.chasmEngine, err = s.GetTestCluster().Host().ChasmEngine()
    s.Require().NoError(err)
}

func (s *CoffeeMachineFunctionalSuite) TestMakeCoffee_CompleteFlow() {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // Create coffee machine
    createResp, err := NewCoffeeMachineHandler(
        chasm.NewEngineContext(ctx, s.chasmEngine),
        NewCoffeeMachineRequest{
            NamespaceID: s.NamespaceID(),
            MachineID:   "test-machine-001",
        },
    )
    s.NoError(err)
    s.Equal("ready", createResp.Status)

    // Make coffee
    makeResp, err := MakeCoffeeHandler(
        chasm.NewEngineContext(ctx, s.chasmEngine),
        MakeCoffeeRequest{
            MachineRef: createResp.MachineRef,
            CoffeeType: "espresso",
            SizeML:     200,
        },
    )
    s.NoError(err)
    s.True(makeResp.Success)

    // Wait for tasks to execute
    time.Sleep(5 * time.Second)

    // Verify final state
    descResp, err := DescribeMachineHandler(
        chasm.NewEngineContext(ctx, s.chasmEngine),
        DescribeMachineRequest{
            MachineRef: createResp.MachineRef,
        },
    )
    s.NoError(err)
    s.Equal(coffeepb.MACHINE_STATE_IDLE, descResp.State)
    s.Equal(int32(1), descResp.CupsMade)
}
```

### Testing Best Practices

1. **Test Transition Validity**: Always test invalid source states
2. **Test Task Generation**: Verify tasks are created with correct attributes
3. **Test State Updates**: Confirm all state fields are updated correctly
4. **Test Failure Paths**: Include tests for error conditions
5. **Mock External Dependencies**: Use mocks for hardware controllers, HTTP clients
6. **Test Nested Components**: Verify component hierarchy works correctly
7. **Test Validators**: Ensure task validators prevent stale task execution
8. **Use Table-Driven Tests**: Cover multiple scenarios efficiently

## CHASM Details

### How CHASM Persists Changes

CHASM persists components as a tree structure where each node is stored as a separate database row. This enables efficient partial loading and updates.

#### Database Schema

Components are stored in the `chasm_node_maps` table (see `schema/mysql/v8/temporal/versioned/v1.17/add_chasm_node_maps.sql`):

```sql
CREATE TABLE chasm_node_maps (
  shard_id INT NOT NULL,
  namespace_id BINARY(16) NOT NULL,
  workflow_id VARCHAR(255) NOT NULL,
  run_id BINARY(16) NOT NULL,
  chasm_path VARBINARY(1536) NOT NULL,  -- Encoded node path

  metadata MEDIUMBLOB NOT NULL,         -- Node metadata (proto)
  metadata_encoding VARCHAR(16),
  data MEDIUMBLOB,                      -- Node data (proto)
  data_encoding VARCHAR(16),

  PRIMARY KEY (shard_id, namespace_id, workflow_id, run_id, chasm_path)
);
```

**Key Points**:
- One row per component node in the tree
- `chasm_path` encodes the node's position (e.g., "" for root, "WaterTank", "Callbacks/callback-1")
- Metadata and data are stored separately for efficient metadata-only queries
- Composite primary key enables sharding and fast lookups

#### Persistence Data Structure

Each persisted node contains (see `proto/internal/temporal/server/api/persistence/v1/chasm.proto`):

```protobuf
message ChasmNode {
    ChasmNodeMetadata metadata = 1;
    temporal.api.common.v1.DataBlob data = 2;
}

message ChasmNodeMetadata {
    VersionedTransition initial_versioned_transition = 1;
    VersionedTransition last_update_versioned_transition = 2;

    oneof attributes {
        ChasmComponentAttributes component_attributes = 11;
        ChasmDataAttributes data_attributes = 12;
        ChasmCollectionAttributes collection_attributes = 13;
        ChasmPointerAttributes pointer_attributes = 14;
    }
}

message ChasmComponentAttributes {
    string type = 1;                                  // Fully qualified component type
    repeated Task side_effect_tasks = 2;             // Ordered by insertion
    repeated Task pure_tasks = 3;                     // Ordered by scheduled time
}
```

**For the Coffee Machine example:**
```
Root Node (path=""):
  - Metadata: type="coffeemachine.CoffeeMachine", tasks=[]
  - Data: CoffeeMachineData proto (state, cups_made, last_clean_time, etc.)

WaterTank Node (path="WaterTank"):
  - Metadata: type="coffeemachine.WaterTank", tasks=[]
  - Data: WaterTankData proto (level_ml, capacity_ml)

BeanHopper Node (path="BeanHopper"):
  - Metadata: type="coffeemachine.BeanHopper", tasks=[]
  - Data: BeanHopperData proto (level_grams, capacity_grams)
```

#### Serialization Flow

When you call `chasm.UpdateComponent()` or `chasm.NewEntity()`, CHASM goes through a transaction flow (see `chasm/tree.go:1854`):

**1. User Transaction Function Executes**
```go
_, _, err := chasm.UpdateComponent(ctx, ref,
    func(cm *CoffeeMachine, ctx chasm.MutableContext, input Request) (Response, error) {
        // Modify component state
        cm.CupsMade++

        // Access nested components
        waterTank, _ := cm.WaterTank.Get(ctx)
        waterTank.LevelMl -= 200

        // Add tasks
        ctx.AddTask(cm, chasm.TaskAttributes{}, &task)

        return response, nil
    },
    request,
)
```

**2. Close Transaction** (`tree.go:1854`)

The framework processes all changes:

```go
func (n *Node) CloseTransaction(nextVT *VersionedTransition) (NodesMutation, error) {
    // Step 1: Sync tree structure
    n.syncSubComponents()

    // Step 2: Resolve cross-references
    n.resolveDeferredPointers()

    // Step 3: Update root lifecycle
    n.closeTransactionHandleRootLifecycleChange()

    // Step 4: Update visibility
    n.closeTransactionForceUpdateVisibility()

    // Step 5: Serialize modified nodes
    n.closeTransactionSerializeNodes()

    // Step 6: Attach tasks to components
    n.closeTransactionUpdateComponentTasks(nextVT)

    // Step 7: Return mutations
    return NodesMutation{
        UpdatedNodes: updatedNodes,  // path -> serialized node
        DeletedNodes: deletedNodes,  // paths to delete
    }, nil
}
```

**3. Sync Tree Structure** (`syncSubComponents()`)

Recursively walk component fields and create/update/delete nodes:

```go
// For each field in the component
for fieldName, field := range componentFields {
    switch field.Type {
    case ComponentField:
        // Create or update child node
        childNode := getOrCreateNode(fieldName)
        childNode.value = field.Value
        childNode.setValueState(valueStateNeedSerialize)

    case MapField:
        // Handle collection
        for key, item := range field.MapValue {
            itemNode := getOrCreateNode(fieldName + "/" + key)
            itemNode.value = item
        }
    }
}

// Delete nodes for removed components
for existingChild := range n.children {
    if !stillExists(existingChild) {
        n.deletions.Add(existingChild.encodedPath)
    }
}
```

**4. Serialize Nodes** (`serialize()`)

Convert in-memory components to protobuf:

```go
func (n *Node) serialize() error {
    // Find the proto message field (embedded in component)
    protoField := findProtoField(n.value)

    // Serialize proto message
    dataBlob, err := proto.Marshal(protoField)

    // Update serialized node
    n.serializedNode = &persistencespb.ChasmNode{
        Metadata: &persistencespb.ChasmNodeMetadata{
            ComponentAttributes: &persistencespb.ChasmComponentAttributes{
                Type: "coffeemachine.CoffeeMachine",
            },
        },
        Data: &commonpb.DataBlob{
            EncodingType: enums.ENCODING_TYPE_PROTO3,
            Data:         dataBlob,
        },
    }

    n.setValueState(valueStateSynced)
    return nil
}
```

**5. Attach Tasks** (`closeTransactionUpdateComponentTasks()`)

Add generated tasks to component metadata:

```go
for component, tasks := range n.newTasks {
    componentNode := findNodeForComponent(component)

    for i, taskItem := range tasks {
        // Serialize task payload
        taskData, _ := proto.Marshal(taskItem.task)

        // Create task metadata
        persistedTask := &ChasmComponentAttributes_Task{
            Type:                      "coffeemachine.grind",
            Destination:               taskItem.attributes.Destination,
            ScheduledTime:            timestamppb.New(taskItem.attributes.ScheduledTime),
            Data:                      taskData,
            VersionedTransition:       nextVT,
            VersionedTransitionOffset: i,
        }

        // Add to component's task list
        if isPureTask {
            componentNode.Metadata.PureTasks = append(..., persistedTask)
        } else {
            componentNode.Metadata.SideEffectTasks = append(..., persistedTask)
        }
    }
}
```

**6. Return Mutations**

Return the map of changed nodes:

```go
NodesMutation{
    UpdatedNodes: {
        "": <serialized CoffeeMachine node>,
        "WaterTank": <serialized WaterTank node>,
    },
    DeletedNodes: {},
}
```

**7. Persist to Database**

The engine writes mutations to the database (see `service/history/chasm_engine.go`):

```go
func (e *EngineImpl) persistNodesMutation(
    ctx context.Context,
    entityKey EntityKey,
    mutation NodesMutation,
) error {
    rows := []ChasmNodeMapsRow{}

    // Add updated nodes
    for path, node := range mutation.UpdatedNodes {
        rows = append(rows, ChasmNodeMapsRow{
            ShardID:          entityKey.ShardID,
            NamespaceID:      entityKey.NamespaceID,
            WorkflowID:       entityKey.WorkflowID,
            RunID:            entityKey.RunID,
            ChasmPath:        encodePath(path),
            Metadata:         node.Metadata,
            MetadataEncoding: "proto3",
            Data:             node.Data,
            DataEncoding:     "proto3",
        })
    }

    // Batch insert/update
    return persistence.ReplaceIntoChasmNodeMaps(ctx, rows)

    // Delete removed nodes
    for path := range mutation.DeletedNodes {
        persistence.DeleteFromChasmNodeMaps(ctx, entityKey, path)
    }
}
```

#### Deserialization Flow

When loading a component (see `chasm/tree.go:2156`):

**1. Load from Database**
```go
rows, err := persistence.SelectAllFromChasmNodeMaps(ctx, entityKey)
// Returns all rows for this workflow execution
```

**2. Build Tree**
```go
serializedNodes := map[string]*persistencespb.ChasmNode{}
for _, row := range rows {
    path := decodePath(row.ChasmPath)
    serializedNodes[path] = &persistencespb.ChasmNode{
        Metadata: row.Metadata,
        Data:     row.Data,
    }
}

tree := chasm.NewTree(serializedNodes, registry, ...)
```

**3. Lazy Deserialize** (`prepareComponentValue()`)
```go
func (n *Node) prepareComponentValue(ctx Context) error {
    if n.valueState != valueStateNeedDeserialize {
        return nil  // Already loaded
    }

    // Get registered component type
    fqType := n.serializedNode.Metadata.ComponentAttributes.Type
    componentDef := n.registry.Component(fqType)

    // Create empty component instance
    component := reflect.New(componentDef.Type).Interface()

    // Deserialize proto data
    protoMsg := componentDef.NewProtoMessage()
    proto.Unmarshal(n.serializedNode.Data.Data, protoMsg)

    // Set proto field on component
    setProtoFieldOnComponent(component, protoMsg)

    // Set component value
    n.value = component
    n.setValueState(valueStateSynced)

    return nil
}
```

#### Value States

Nodes track synchronization state (see `chasm/tree.go:120`):

```go
type valueState int

const (
    valueStateNeedDeserialize  // Persisted but not loaded
    valueStateSynced           // In sync with database
    valueStateNeedSerialize    // Modified, needs serialization
    valueStateNeedSyncStructure // Children changed
)
```

This enables:
- **Lazy loading**: Only deserialize nodes when accessed
- **Dirty tracking**: Only serialize modified nodes
- **Efficient updates**: Only persist changed components

#### Versioned Transitions

Every mutation includes a `VersionedTransition` for conflict detection (see `chasm/tree.go:1762`):

```go
type VersionedTransition struct {
    TransitionCount int64  // Monotonically increasing counter
}

// On update:
nextVT := &VersionedTransition{
    TransitionCount: currentVT.TransitionCount + 1,
}

// Stored in metadata for each component
node.Metadata.LastUpdateVersionedTransition = nextVT
```

This enables:
- **Conflict detection**: Detect concurrent modifications in replication
- **Task ordering**: Tasks tagged with transition that created them
- **Causality tracking**: Track which transition created which state changes

### How CHASM Generates Tasks

CHASM generates two types of tasks: **side-effect tasks** (have external effects) and **pure tasks** (only mutate CHASM state). Tasks are generated during transitions and persisted with the component.

#### Task Types

**Side-Effect Tasks** (see `service/history/tasks/chasm_task.go`):
- Make HTTP calls, write to databases, send messages
- Execute asynchronously via task processors
- Cannot execute immediately (require external resources)
- Example: `GrindBeansTask` that controls hardware

**Pure Tasks** (see `service/history/tasks/chasm_task_pure.go`):
- Only modify CHASM component state
- No external side effects
- Can execute immediately within transaction if scheduled time is "now"
- Example: `CheckSuppliesTask` that updates internal state

#### Task Generation Flow

**1. Generate Task During Transition**

```go
var TransitionStartGrinding = chasm.NewTransition(
    []coffeepb.MachineState{coffeepb.MACHINE_STATE_IDLE},
    coffeepb.MACHINE_STATE_GRINDING,
    func(cm *CoffeeMachine, ctx chasm.MutableContext, event EventStartBrewing) error {
        // Generate side-effect task
        ctx.AddTask(cm, chasm.TaskAttributes{
            ScheduledTime: time.Time{},  // Immediate
            Destination:   "",             // Local
        }, &coffeepb.GrindBeansTask{
            Grams: 20,
        })

        // Generate scheduled pure task
        ctx.AddTask(cm, chasm.TaskAttributes{
            ScheduledTime: ctx.Now(cm).Add(2 * time.Hour),
        }, &coffeepb.CheckSuppliesTask{})

        return nil
    },
)
```

**2. Store Task in Tree** (`chasm/tree.go:1600`)

```go
func (n *Node) AddTask(component Component, attrs TaskAttributes, task any) {
    taskType := n.registry.TaskFor(task)

    if taskType.IsPure && attrs.IsImmediate() {
        // Immediate pure tasks execute in current transaction
        n.immediatePureTasks[component] = append(n.immediatePureTasks[component], task)
    } else {
        // Regular tasks stored for persistence
        n.newTasks[component] = append(n.newTasks[component], taskWithAttributes{
            task:       task,
            attributes: attrs,
        })
    }
}
```

**3. Execute Immediate Pure Tasks** (`executeImmediatePureTasks()`)

```go
func (n *Node) executeImmediatePureTasks() error {
    for component, tasks := range n.immediatePureTasks {
        taskNode := n.findNodeForComponent(component)

        for _, task := range tasks {
            executor := n.registry.PureTaskExecutor(task)

            // Execute within transaction
            err := executor.Execute(n.getMutableContext(), component, task)
            if err != nil {
                return err
            }
        }
    }
    return nil
}
```

**4. Persist Tasks with Component** (`closeTransactionUpdateComponentTasks()`)

```go
func (n *Node) closeTransactionUpdateComponentTasks(nextVT *VersionedTransition) {
    for component, tasks := range n.newTasks {
        componentNode := n.findNodeForComponent(component)

        for offset, taskItem := range tasks {
            // Serialize task payload
            taskData, _ := proto.Marshal(taskItem.task)
            taskType := n.registry.TaskFor(taskItem.task)

            // Create persisted task
            persistedTask := &ChasmComponentAttributes_Task{
                Type:                      taskType.FullyQualifiedName(),
                Destination:               taskItem.attributes.Destination,
                ScheduledTime:             timestamppb.New(taskItem.attributes.ScheduledTime),
                Data:                      &DataBlob{Data: taskData},
                VersionedTransition:       nextVT,
                VersionedTransitionOffset: int64(offset),
                PhysicalTaskStatus:        physicalTaskStatusNone,
            }

            // Add to component metadata
            if taskType.IsPure {
                componentNode.Metadata.ComponentAttributes.PureTasks =
                    append(..., persistedTask)
            } else {
                componentNode.Metadata.ComponentAttributes.SideEffectTasks =
                    append(..., persistedTask)
            }
        }
    }
}
```

**5. Create Physical Tasks** (`service/history/chasm_engine.go`)

Convert CHASM component tasks to physical task queue tasks:

```go
func (e *EngineImpl) createPhysicalTasks(
    entityKey EntityKey,
    mutation NodesMutation,
) []tasks.Task {
    physicalTasks := []tasks.Task{}

    for path, node := range mutation.UpdatedNodes {
        componentTasks := node.Metadata.ComponentAttributes.SideEffectTasks

        for _, componentTask := range componentTasks {
            // Determine task category based on schedule time
            category := tasks.CategoryTransfer
            if !componentTask.ScheduledTime.AsTime().IsZero() {
                category = tasks.CategoryTimer
            }

            // Create physical task
            physicalTask := &tasks.ChasmTask{
                WorkflowKey:         entityKey.ToWorkflowKey(),
                TaskID:              generateTaskID(),
                VisibilityTimestamp: componentTask.ScheduledTime.AsTime(),
                Category:            category,

                Info: &persistencespb.ChasmTaskInfo{
                    ComponentPath: path,
                    TaskType:      componentTask.Type,
                    Data:          componentTask.Data,
                    Destination:   componentTask.Destination,
                },
            }

            physicalTasks = append(physicalTasks, physicalTask)
        }

        // Same for pure tasks
        for _, pureTask := range node.Metadata.ComponentAttributes.PureTasks {
            physicalTasks = append(physicalTasks, &tasks.ChasmTaskPure{...})
        }
    }

    return physicalTasks
}
```

**6. Task Execution**

When the task processor picks up a task:

```go
func (e *ChasmTaskExecutor) Execute(ctx context.Context, task *tasks.ChasmTask) error {
    // Deserialize task payload
    taskPayload := deserializeTask(task.Info.TaskType, task.Info.Data)

    // Build component reference
    ref := chasm.ComponentRef{
        EntityKey:     task.WorkflowKey.ToEntityKey(),
        ComponentPath: task.Info.ComponentPath,
    }

    // Get task executor
    executor := e.registry.SideEffectTaskExecutor(task.Info.TaskType)

    // Validate task is still relevant
    valid, err := chasm.ReadComponent(ctx, ref,
        func(ctx chasm.Context, component Component, _ struct{}) (bool, error) {
            return executor.Validate(ctx, component, task.Attributes, taskPayload)
        },
        struct{}{},
    )

    if !valid {
        // Task is stale, skip execution
        return nil
    }

    // Execute task
    err = executor.Execute(ctx, ref, task.Attributes, taskPayload)

    // Delete task from component metadata (for pure tasks)
    if task.IsPure {
        chasm.UpdateComponent(ctx, ref, deleteCompletedTask, task)
    }

    return err
}
```

#### Task Attributes

Tasks have scheduling and routing attributes (see `chasm/task.go:26`):

```go
type TaskAttributes struct {
    ScheduledTime time.Time  // When to execute
    Destination   string     // For routing (e.g., HTTP endpoint)
}

func (a *TaskAttributes) IsImmediate() bool {
    return a.ScheduledTime.IsZero() ||
           a.ScheduledTime.Equal(TaskScheduledTimeImmediate)
}
```

**Examples**:
```go
// Immediate task
ctx.AddTask(component, chasm.TaskAttributes{}, task)

// Scheduled task (timer)
ctx.AddTask(component, chasm.TaskAttributes{
    ScheduledTime: time.Now().Add(1 * time.Hour),
}, task)

// Outbound task (to external service)
ctx.AddTask(component, chasm.TaskAttributes{
    Destination: "https://api.example.com/callback",
}, task)
```

#### Task Validation

Executors implement validators to prevent stale task execution:

```go
type TaskValidator[C any, T any] interface {
    Validate(Context, C, TaskAttributes, T) (bool, error)
}

// Example: Only execute if attempt number matches
func (e *GrindBeansExecutor) Validate(
    ctx chasm.Context,
    cm *CoffeeMachine,
    attrs chasm.TaskAttributes,
    task *coffeepb.GrindBeansTask,
) (bool, error) {
    // Only valid if still in GRINDING state
    return cm.State == coffeepb.MACHINE_STATE_GRINDING, nil
}
```

This prevents:
- Executing tasks after component state has changed
- Duplicate execution on retries
- Running tasks for deleted components

#### Task Lifecycle

```
1. Generate:    ctx.AddTask() in transition
                     ↓
2. Store:       Added to n.newTasks map
                     ↓
3. Persist:     Serialized to component metadata
                     ↓
4. Convert:     Create physical task (ChasmTask)
                     ↓
5. Schedule:    Insert into task queue
                     ↓
6. Execute:     Task processor picks up task
                     ↓
7. Validate:    Check if task still relevant
                     ↓
8. Run:         Execute task logic
                     ↓
9. Update:      Update component state
                     ↓
10. Clean:      Delete pure tasks from metadata
```

#### Task Deletion

Pure tasks are deleted after successful execution (see `chasm/tree.go:1900`):

```go
func (n *Node) DeleteCHASMPureTasks(maxScheduledTime time.Time) {
    tasks := n.serializedNode.Metadata.ComponentAttributes.PureTasks

    // Keep only tasks scheduled after maxScheduledTime
    remaining := []Task{}
    for _, task := range tasks {
        if task.ScheduledTime.AsTime().After(maxScheduledTime) {
            remaining = append(remaining, task)
        }
    }

    n.serializedNode.Metadata.ComponentAttributes.PureTasks = remaining
    n.setValueState(valueStateNeedSerialize)
}
```

Side-effect tasks remain in metadata for audit/debugging purposes.

#### Coffee Machine Task Example

For the coffee machine making one cup:

```
Transaction 1: MakeCoffee called
  → Transition: IDLE → GRINDING
  → Generate: GrindBeansTask (immediate, side-effect)
  → Persist: Task added to CoffeeMachine node metadata
  → Create: Transfer task in queue

Task Processor: Execute GrindBeansTask
  → Validate: Machine still in GRINDING state
  → Execute: Control hardware grinder
  → Update: Transition GRINDING → BREWING
  → Generate: BrewCoffeeTask

Task Processor: Execute BrewCoffeeTask
  → Execute: Control brewing hardware
  → Update: Transition BREWING → DISPENSING
  → Generate: DispenseCoffeeTask

Task Processor: Execute DispenseCoffeeTask
  → Execute: Dispense coffee
  → Update: Transition DISPENSING → IDLE
  → Generate: CheckSuppliesTask (scheduled +2 hours, pure)

Timer Task Processor (2 hours later): Execute CheckSuppliesTask
  → Validate: Always valid
  → Execute: Check supplies in CHASM state
  → Update: Trigger cleaning if needed
  → Delete: Remove CheckSuppliesTask from metadata
```

This design ensures:
- Tasks are never lost (persisted with component)
- Tasks execute in correct order (scheduled times)
- Stale tasks are filtered (validation)
- State mutations are transactional (CHASM context)
- Pure tasks clean up after execution