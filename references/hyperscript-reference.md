# \_hyperscript Language Reference for AI Context

## Overview

_hyperscript is a scripting language for HTML, designed for DOM manipulation and event handling.
It uses English-like syntax embedded in HTML attributes: _="..." or data-script="...".

---

## Script Placement

On element:
<button _="on click add .active to me">Click</button>

In script tag:

<script type="text/hyperscript">
def greet(name)
return "Hello, " + name
end
</script>

---

## Event Handlers

### Basic Syntax

    on <event> [from <source>] [<filters>] <commands>

### Events

    on click ...
    on mouseenter ...
    on mouseleave ...
    on keyup ...
    on keydown ...
    on submit ...
    on change ...
    on input ...
    on load ...
    on scroll ...
    on focus ...
    on blur ...
    on htmx:afterSwap ...
    on htmx:beforeRequest ...
    on custom-event ...

### Event Modifiers

    on click[button==0]           -- left click only
    on keyup[key=='Enter']        -- specific key
    on click[ctrlKey]             -- with ctrl held
    on click[shiftKey]            -- with shift held
    on submit[target.checkValidity()] -- conditional

### Event Sources

    on click from window ...
    on click from document ...
    on click from closest .parent ...
    on click from #some-id ...
    on resize from window ...
    on custom-event from body ...

### Debounce and Throttle

    on input debounced at 300ms ...
    on scroll throttled at 100ms ...

### Queue Control

    on click queue all ...        -- queue all events
    on click queue first ...      -- ignore if running
    on click queue last ...       -- replace queued
    on click queue none ...       -- discard if running (default)

### Every (Continuous Listener)

    every click ...               -- does not consume event
    every intersection ...        -- for intersection observer

---

## Commands

### Variable Assignment

    set x to 5
    set myVar to "hello"
    set el to #myElement
    set items to <li/> in me
    set data to {name: "John", age: 30}
    set arr to [1, 2, 3]

    -- Local vs element-scoped
    set :counter to 0             -- element-scoped (persists)
    set $global to "value"        -- global scope
    set x to 0                    -- local to handler

### DOM Manipulation

    -- Classes
    add .active to me
    add .highlight to #target
    add .one .two to <li/> in me
    remove .active from me
    remove .hidden from #modal
    toggle .open on me
    toggle .visible on #dropdown
    toggle .active on me for 2s   -- temporary toggle

    -- Attributes
    set @disabled to "true"
    set @href of #link to "/new"
    remove @disabled from me
    set my @data-id to "123"

    -- Properties
    set my.innerHTML to "<b>Hi</b>"
    set value of #input to ""
    set #checkbox.checked to true

    -- Content
    put "Hello" into me
    put "World" into #target
    put "<b>HTML</b>" into me
    put "Prepend" at start of me
    put "Append" at end of me
    put "Before" before me
    put "After" after me

### Show/Hide

    hide me
    hide #modal
    hide me with opacity
    show me
    show #modal
    show me with display:flex     -- specify display type
    show me with *opacity         -- animate opacity

    toggle visibility of me
    toggle visibility of #panel

### Creating/Removing Elements

    make <div.card/>
    make <button.btn/> called newBtn
    make <li/> then put it at end of #list

    remove me
    remove #element
    remove .temp from document

### Waiting

    wait 1s
    wait 500ms
    wait 2s then remove me
    wait for click
    wait for htmx:afterSwap
    wait for customEvent from #other

### Fetching Data

    fetch /api/data then put result into me
    fetch /api/data as json then set items to result
    fetch /api/data with method:"POST", body:{x:1} then ...
    fetch /api/users/{userId} as json then ...

    -- With headers
    fetch /api/data with {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(data)
    } as json then ...

### Calling Functions

    call alert("Hello")
    call myFunction()
    call myObject.method(arg1, arg2)
    call #element.focus()
    call console.log("debug", myVar)

    js return new Date().toISOString() end  -- inline JS
    set now to js return Date.now() end

### Sending Events

    send click to #button
    send custom-event to #target
    send myEvent(detail: {foo: "bar"}) to body
    send refresh to <.item/>      -- send to all matches
    trigger click on #button
    trigger submit on closest <form/>

### Transitions

    transition my opacity to 0 over 500ms
    transition #el's height to "0px" over 300ms
    settle                        -- wait for CSS transitions

### Focus and Selection

    focus()                       -- focus me
    focus() on #input
    blur()
    select()                      -- select text input content

### Navigation

    go to url /new-page
    go to url /page in new window
    go to top of me               -- scroll
    go to bottom of #container
    go to middle of #section smoothly

### Logging

    log me
    log myVar
    log "Message:", data

### Throwing/Halting

    halt                          -- stop current handler
    halt the event                -- stop + preventDefault
    halt the event's bubbling     -- stopPropagation
    throw "Error message"

---

## Control Flow

### Conditionals

    if x > 5
      add .big to me
    else if x > 0
      add .small to me
    else
      add .zero to me
    end

    -- Inline
    if I match .active add .highlight to me
    if @disabled of me remove me
    if result is not empty put result into me

### Comparison Operators

    x == 5
    x is 5                        -- same as ==
    x is not 5
    x != 5
    x < 5
    x <= 5
    x > 5
    x >= 5
    x is empty
    x is not empty
    x exists
    x does not exist
    x matches .selector
    x does not match .active
    no x                          -- x is null/undefined

### Logical Operators

    x and y
    x or y
    not x

### Loops

    repeat 5 times
      log "Hello"
    end

    repeat while x < 10
      set x to x + 1
    end

    repeat until done
      call processNext()
    end

    repeat for item in items
      log item
    end

    repeat for char in "hello"
      log char
    end

    repeat in items
      log it
    end

### Loop Index

    repeat for item in items index i
      log i, item
    end

---

## Expressions and Selectors

### DOM References

    me                            -- current element
    my                            -- possessive form of me
    I                             -- alias for me
    it                            -- last result
    its                           -- possessive of it
    result                        -- last command result

    #myId                         -- getElementById
    .myClass                      -- first match
    <button/>                     -- first button
    <.myClass/>                   -- all matches (array)
    <li/> in me                   -- descendants
    <li/> in #list                -- descendants of #list
    <input[type="text"]/>         -- attribute selector

    closest .parent               -- closest ancestor
    closest <form/>
    first in <li/>                -- first match
    last in <li/>                 -- last match
    random in <.card/>            -- random element

### Possessives

    my.innerHTML
    my @href
    my.classList
    the innerHTML of me
    the value of #input
    its length
    the @href of closest <a/>
    the first of <li/> in me

### Attribute Access

    @href                         -- my @href
    @data-id of #el
    @disabled of me

### Property Access

    me.value
    #input.checked
    its.length
    element's children

### String Templates

    set msg to `Hello ${name}!`
    put `<li>${item}</li>` at end of #list

### Math

    x + 1
    x - 1
    x * 2
    x / 2
    x % 2

### String Operations

    "hello" + " world"
    "hello" contains "ell"        -- true
    "hello" starts with "he"      -- true
    "hello" ends with "lo"        -- true

### Arrays

    first in arr
    last in arr
    random in arr
    arr[0]
    length of arr
    item is in arr

### Null Coalescing

    x or "default"
    @data-val of me or "none"

---

## Built-in Variables

### Event Context (in handlers)

    event                         -- the event object
    event.target                  -- element that fired event
    event.detail                  -- custom event data
    event.key                     -- key pressed
    event.clientX                 -- mouse position
    event.preventDefault()
    event.stopPropagation()

### Shorthand

    target                        -- event.target
    detail                        -- event.detail

### Global

    body                          -- document.body
    document
    window

---

## Async Behavior

### Commands that wait

These commands are async and execution pauses until complete:
wait 1s
fetch /api
settle
transition
wait for <event>

### Parallel Execution

    send foo to #a
    send bar to #b    -- both fire immediately

    -- To wait:
    send foo to #a then wait 100ms then send bar to #b

---

## Features (Behaviors)

### Defining Behaviors

    behavior Draggable
      on mousedown
        -- drag logic
      end
    end

    behavior Closeable
      on click from .close in me
        remove me
      end
    end

### Using Behaviors

    <div _="install Draggable install Closeable">...</div>

### Defining Functions

    def greet(name)
      return `Hello, ${name}!`
    end

    def add(a, b)
      return a + b
    end

### Calling Defined Functions

    set message to greet("World")
    call greet("User")

---

## Time Literals

    1s                            -- 1 second
    500ms                         -- 500 milliseconds
    2.5s                          -- 2.5 seconds
    100ms                         -- 100 milliseconds

---

## HTMX Integration

### Common HTMX Events

    on htmx:beforeRequest ...
    on htmx:afterRequest ...
    on htmx:beforeSwap ...
    on htmx:afterSwap ...
    on htmx:afterSettle ...
    on htmx:sendError ...
    on htmx:responseError ...
    on htmx:configRequest ...
    on htmx:load ...

### Working with HTMX

    -- Disable button during request
    on click
      add @disabled to me
      htmx.trigger(me, "doLoad")

    on htmx:afterRequest from me
      remove @disabled from me

    -- Respond to swapped content
    on htmx:afterSwap
      call initializeComponent(event.detail.elt)

---

## Common Patterns

### Toggle Visibility

    <button _="on click toggle .hidden on #panel">Toggle</button>

### Form Submission Feedback

    <form _="on submit add @disabled to <button/> in me
             on htmx:afterRequest remove @disabled from <button/> in me">

### Click Outside to Close

    <div class="modal" _="on click from elsewhere hide me">

### Debounced Search

    <input _="on input debounced at 300ms send search to #results">

### Confirm Before Action

    <button _="on click if confirm('Sure?') send delete to me">Delete</button>

### Temporary Message

    <div _="on showMessage(msg)
            put msg into me
            show me
            wait 3s
            hide me">
    </div>

### Loading State

    <button _="on click
               add .loading to me
               fetch /api/action then put result into #output
               remove .loading from me">
      Load
    </button>

### Keyboard Shortcuts

    <body _="on keydown[key=='Escape'] send close to .modal
             on keydown[key=='k' and ctrlKey] focus() on #search">

### Infinite Scroll

    <div _="on intersection(intersecting)
            if intersecting fetch /more then put result at end of #list">
    </div>

### Element-Scoped Counter

    <button _="on click
               set :count to (:count or 0) + 1
               put :count into next <span/>">
      Clicked: <span>0</span>
    </button>

---

## Gotchas

1. set x to ... is local; use :x for element-scoped persistence
2. <.class/> returns array; first in <.class/> for single element
3. toggle .x on me not toggle .x to me
4. Use halt the event to preventDefault, not just halt
5. wait for pauses execution; send does not wait for response
6. Selector <div/> requires angle brackets; div alone is a variable
7. String interpolation uses backticks: `${var}`
8. Comments use -- not // or /\* \*/
9. end keyword closes blocks (if, repeat, behavior, def)
10. Chaining uses then: fetch /api then put result into me

---

## Patterns & Anti-Patterns

### State Management

DON'T - Local variable resets each event:
on click set count to count + 1

DO - Element-scoped persists:
on click set :count to (:count or 0) + 1

### Toggling Classes

DON'T - Redundant logic:
on click if I match .active remove .active from me else add .active to me end

DO - Use toggle:
on click toggle .active on me

### Targeting Multiple Elements

DON'T - Only hits first match:
on click add .highlight to .item

DO - Use query all:
on click add .highlight to <.item/>

### Removing Element After Action

DON'T - Element gone before transition:
on click remove me then wait 300ms

DO - Wait then remove:
on click add .fade-out to me then wait 300ms then remove me

### Conditional Visibility

DON'T - Inline style manipulation:
on click set my.style.display to 'none'

DO - Use hide/show or classes:
on click hide me
on click add .hidden to me

### Getting Input Values

DON'T - Verbose property access:
on click set val to the value of #input

DO - Direct access:
on click set val to #input.value

### Event Delegation

DON'T - Handler on each child:

<li _="on click ..."> <!-- repeated N times -->

DO - Single handler on parent:

<ul _="on click from <li/> in me ...">

### Checking Existence

DON'T - Verbose null check:
on click if #element is not null ...

DO - Use exists:
on click if #element exists ...

### String Building in DOM

DON'T - Complex concatenation:
set html to '<div class="card"><h2>' + title + '</h2><p>' + body + '</p></div>'

DO - Use template literals:
set html to `<div class="card"><h2>${title}</h2><p>${body}</p></div>`

### Multiple Event Handlers

DON'T - Separate attributes:

<div _="on click ..." _="on mouseenter ..."> <!-- second overwrites first -->

DO - Chain in one attribute:

<div _="on click ... on mouseenter ...">

### Waiting for User Confirmation

DON'T - Blocking without feedback:
on click call confirm('Delete?') if result delete()

DO - Proper conditional:
on click if confirm('Delete?') call delete() end

---

## HTMX + Hyperscript Timing Guide

### Event Sequence

HTMX fires events in this order:

    1. htmx:configRequest  -- modify request headers/params before send
    2. htmx:beforeRequest  -- request about to send (can cancel)
    3. htmx:beforeSend     -- XHR about to send
    4. htmx:afterRequest   -- response received (success or error)
    5. htmx:beforeSwap     -- about to swap content (can modify)
    6. htmx:afterSwap      -- DOM updated, scripts not run yet
    7. htmx:afterSettle    -- CSS transitions complete
    8. htmx:load           -- fired on new content (like DOMContentLoaded)

### When to Use Each Event

htmx:beforeRequest - Show loading state - Disable buttons - Validate before send - Cancel request conditionally

htmx:afterRequest - Hide loading state - Re-enable buttons - Handle errors - Fires for both success AND error

htmx:afterSwap - React to new content in DOM - Content exists but transitions may be running - New elements queryable

htmx:afterSettle - Transitions complete - Safe to measure layout - Safe to focus elements - Best for initializing swapped content

htmx:load - Initialize new content - Like DOMContentLoaded for swapped content - Fires on the new elements themselves

### Common Timing Patterns

#### Disable Button During Request

    on click
      add @disabled to me
      add .loading to me
    on htmx:afterRequest from me
      remove @disabled from me
      remove .loading from me

#### Global Loading Spinner

    <div id="spinner" class="hidden"
         _="on htmx:beforeRequest from body
              add .visible to me
            on htmx:afterRequest from body
              remove .visible from me">

#### Initialize Swapped Content

    on htmx:afterSettle from body
      call initializeNewContent(event.detail.elt)

#### Wait for Swap Before Accessing New Elements

DON'T - Race condition, new content not in DOM yet:
on click
send loadContent to #container
set val to #newElement.value -- may not exist

DO - Wait for swap event:
on click send loadContent to #container
on htmx:afterSwap from #container
set val to #newElement.value

#### Error Handling

    on htmx:responseError from body
      put event.detail.xhr.status into #error-code
      show #error-banner

    on htmx:sendError from body
      put 'Network error' into #error-message
      show #error-banner

#### Prevent Double Submit

    on click
      if I match .submitting halt the event end
      add .submitting to me
    on htmx:afterRequest from me
      remove .submitting from me

#### Optimistic UI with Rollback

    on click
      set :oldContent to my.innerHTML
      put 'Saving...' into me
    on htmx:afterRequest from me
      if event.detail.successful
        put 'Saved!' into me
        wait 1s then put :oldContent into me
      else
        put :oldContent into me
        add .error to me
      end

#### Focus After Swap

    on htmx:afterSettle from #form-container
      focus() on first <input/> in #form-container

### Scope Gotchas

#### Event Source Matters

    -- Only fires for requests FROM this element:
    on htmx:afterSwap from me

    -- Fires for ANY request in document:
    on htmx:afterSwap from body

    -- Fires for requests from descendants:
    on htmx:afterSwap from <button/> in me

#### Swapped Elements Lose Handlers

Problem:
Element with \_="..." is replaced by HTMX swap.
New element has no handlers because it's new DOM.

Solutions: 1. Use hx-swap="innerHTML" to preserve container with handler 2. Put handler on parent that isn't swapped 3. Use behaviors with install on new content 4. Use event delegation from stable ancestor

Example - Handler on stable parent:

<div id="container" _="on click from <button.delete/> in me ...">
<!-- buttons inside can be swapped, handler persists -->
</div>

Example - Behavior for swapped content:

<script type="text/hyperscript">
behavior Deletable
on click
fetch `/delete/${my @data-id}` then remove me
end
end
</script>

    <!-- Server returns new elements with behavior installed -->
    <div _="install Deletable" data-id="123">...</div>

#### afterSwap vs afterSettle

afterSwap: - DOM updated - CSS transitions may still be running - Don't measure layout yet

afterSettle: - Transitions complete - Safe to measure dimensions - Safe to start new animations - Safe to manage focus

#### Request vs Target Element

    event.detail.elt        -- element that triggered request
    event.detail.target     -- element being swapped into
    event.detail.requestConfig.triggeringEvent  -- original event

---

## AI Prompting Guidelines

When asking AI for Hyperscript code, include these constraints:

### State Management

    - Use element-scoped variables (:var) for state that persists across events
    - Use local variables (set x to ...) only for temporary values within a handler
    - Use $global only when truly needed across elements

### Event Handling

    - Use event delegation for repeated/dynamic elements
    - Prefer "from" clause to scope event sources
    - Use debounced/throttled for input and scroll events

### HTMX Integration

    - Handle htmx:afterRequest for cleanup, not just the triggering event
    - Wait for htmx:afterSettle before accessing swapped content dimensions
    - Put handlers on stable ancestors when content will be swapped
    - Use behaviors for reusable patterns on dynamic content

### DOM Manipulation

    - Prefer toggle/show/hide over direct style manipulation
    - Use <.class/> (with angle brackets) to select all matches
    - Use "closest" for finding ancestor elements
    - Use "in me" to scope selectors to descendants

### Code Style

    - Chain related handlers in one _="" attribute
    - Use "then" for sequential operations
    - Use "wait for" to pause for events, "wait Ns" for time delays
    - Use halt the event (not just halt) to preventDefault

---

## Project-Specific Patterns

Add your proven solutions here as you develop them.

### Modal Dialog with HTMX Content

    <div id="modal" class="hidden"
         _="on openModal(url)
              fetch url then put result into #modal-content
              remove .hidden from me
              wait 50ms then focus() on first <input/> in me
            on closeModal
              add .hidden to me
            on click
              if target is me trigger closeModal end
            on keydown[key=='Escape'] from window
              if I do not match .hidden trigger closeModal end">
      <div id="modal-content"></div>
    </div>

### Flash Message Auto-Dismiss

    <div class="flash"
         _="on load wait 3s then add .fade-out to me then wait 300ms then remove me">

### Confirm Before Delete

    <button _="on click
                if confirm('Are you sure?')
                  add .deleting to closest <tr/>
                  fetch `/delete/${my @data-id}` with method:'DELETE'
                  if result.ok
                    remove closest <tr/>
                  else
                    remove .deleting from closest <tr/>
                    call alert('Delete failed')
                  end
                end">
      Delete
    </button>

### Toggle Panel with Persistence

    <button _="on click
                toggle .hidden on #panel
                set @aria-expanded to (#panel matches .hidden) ? 'false' : 'true'">
      Toggle
    </button>

### Form Validation Feedback

    <form _="on submit
               set :valid to true
               for input in <input[required]/> in me
                 if input.value is empty
                   add .error to input
                   set :valid to false
                 else
                   remove .error from input
                 end
               end
               if not :valid halt the event end">

### Debounced Search

    <input type="search"
           _="on input debounced at 300ms
                if my.value.length > 2
                  fetch `/search?q=${my.value}` then put result into #results
                else
                  put '' into #results
                end">

### Keyboard Navigation

    <ul _="on keydown[key=='ArrowDown']
             set current to first <li.active/> in me
             if current exists
               remove .active from current
               add .active to next <li/> from current or first <li/> in me
             else
               add .active to first <li/> in me
             end
           on keydown[key=='ArrowUp']
             set current to first <li.active/> in me
             if current exists
               remove .active from current
               add .active to previous <li/> from current or last <li/> in me
             else
               add .active to last <li/> in me
             end
           on keydown[key=='Enter']
             if first <li.active/> in me exists
               click() on first <li.active/> in me
             end">

### Copy to Clipboard

    <button _="on click
                call navigator.clipboard.writeText(#code-block.textContent)
                set :original to my.innerHTML
                put 'Copied!' into me
                wait 2s then put :original into me">
      Copy
    </button>

---

## Debugging Tips

### Log Everything

    on click
      log "clicked" me event
      -- your code here

### Check Event Details

    on htmx:afterRequest
      log "request complete" event.detail

### Verify Selectors

    on click
      set els to <.target/>
      log "found elements:" els (length of els)

---

## Tooling: Validator Usage

Use the automated CLI validator to check Hyperscript syntax in templates. This section is for tooling, not language semantics.

Commands:

- Validate all templates: `go run ./scripts/validate-hyperscript.go web/templates`
- Validate one file: `go run ./scripts/validate-hyperscript.go web/templates/<file>.html.tmpl`
- Errors only: `go run ./scripts/validate-hyperscript.go -quiet web/templates`
- JSON output: `go run ./scripts/validate-hyperscript.go -json web/templates`
- Custom extensions: `go run ./scripts/validate-hyperscript.go -ext=".html,.tmpl,.gohtml" web/templates`
- Local hyperscript.js: `go run ./scripts/validate-hyperscript.go -hyperscript=third_party/_hyperscript.min.js web/templates`

Behavior:

- Exit code 0 if all snippets are valid (or none found); 1 if any invalid.
- Supports `_="..."`, `_='...'`, and `<script type="text/hyperscript"> ... </script>` blocks.
- Decodes common HTML entities (e.g., `&quot;`) in attributes before parsing.

Workflow:

- Prefer validating the specific file you changed for fast feedback.
- Fix reported errors respecting Go `html/template` quoting rules (use `'...'` outer + `&quot;` inner), then re-run.
- Makefile shortcut: `make validate-hyperscript`.

See HYPSCRIPT_VALIDATION.md for full details.

### Trace Execution

    on click
      log "step 1"
      set x to #input.value
      log "step 2, x=" x
      if x is empty
        log "x was empty, halting"
        halt
      end
      log "step 3, continuing"

### Check Element State

    on click
      log "matches .active?" (I match .active)
      log "disabled?" (my @disabled)
      log "visible?" (my.offsetParent is not null)
