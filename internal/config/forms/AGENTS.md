# Forms Package - Agent Notes

## tview Deadlock / Freeze Issue

This is a **recurring problem**. Read this before touching event handling or redraw logic.

### The Rule

tview event handlers (`SetChangedFunc`, dropdown callbacks, button callbacks, `SetInputCapture`) run on the **main goroutine**. Inside these handlers:

- **SAFE**: Directly modify primitives (add/remove items, change text, etc.). tview redraws automatically after the handler returns.
- **DEADLOCK**: Calling `app.Draw()`, `app.QueueUpdate()`, or `app.QueueUpdateDraw()` from inside an event handler. These block waiting for the main goroutine, which is already busy running the handler. Instant deadlock. The UI freezes.

Source: https://github.com/rivo/tview/wiki/Concurrency

### What Works

`QueueUpdateDraw` is ONLY safe from a **separate goroutine** — never from an event handler callback. Example from our logging hook (tview.go ~line 330):

```go
go func() {
    app.QueueUpdateDraw(func() {
        fmt.Fprint(logPanel, line)
    })
}()
```

This works because it's in a `go func()` — a different goroutine. The logging callback is not an event handler.

### What Does NOT Work

Any of these inside a dropdown/button/input callback:

```go
// DEADLOCK - QueueUpdateDraw from event handler
form.AddDropDown("Provider", options, 0, func(option string, index int) {
    app.QueueUpdateDraw(func() {  // BOOM - freezes
        form.Clear(true)
        rebuildForm()
    })
})

// DEADLOCK - same thing wrapped in goroutine with delay
form.AddDropDown("Provider", options, 0, func(option string, index int) {
    go func() {
        time.Sleep(100 * time.Millisecond)
        app.QueueUpdateDraw(func() {  // Still risky - tview may still hold refs to cleared items
            form.Clear(true)
            rebuildForm()
        })
    }()
})
```

The goroutine+delay approach has been tried and does not reliably work. The dropdown that triggered the callback is being cleared/rebuilt while tview still holds internal references to it. Race conditions, panics, or silent freezes result.

### Dynamic ShowWhen (The Specific Problem)

We have `ShowWhen` conditions on form sections (e.g., `ShowWhen: "provider=whispercpp"` shows Whisper settings only when provider is whispercpp). Currently this is **static** — evaluated once at form build time.

Making it **dynamic** (sections appear/disappear when dropdown changes) requires rebuilding the form from inside a dropdown callback. This is where the deadlock hits.

**Safe approach**: Call `form.Clear(false)` and re-add all fields directly inside the dropdown callback — no `QueueUpdateDraw`, no goroutines. tview will redraw automatically after the handler returns. The `false` parameter to `Clear` preserves buttons.

**Critical caveat — re-entrancy guard required**: tview's `AddDropDown` internally calls `SetCurrentOption()` which fires the selection callback **during form construction** (at `form.go:341`). Without a guard, this causes infinite recursion: build → callback → rebuild → build → ... → stack overflow. The fix is a `rebuilding` boolean flag that prevents re-entrant rebuilds:

```go
rebuilding := false
var rebuildForm func()
rebuildForm = func() {
    if rebuilding {
        return
    }
    rebuilding = true
    form.Clear(true)
    populateFormSections(form, sections, rv, showWhenFields, rebuildForm)
    rebuilding = false
}
// Initial population also needs the guard
rebuilding = true
populateFormSections(form, sections, rv, showWhenFields, rebuildForm)
rebuilding = false
```

### Files with tview Gotcha Comments

- `tview.go` lines 1-7 — package-level gotcha comments
- `tview_wizard.go` lines 1-5 — same warning about QueueEvent(nil)

### History

This issue has bitten us multiple times. Each time someone adds dynamic form behavior, it freezes the TUI. The fix is always the same: remove the `QueueUpdateDraw` / `Draw` / `QueueUpdate` call from the event handler path.
