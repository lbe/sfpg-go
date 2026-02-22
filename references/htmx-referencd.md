# HTMX Reference for AI Context

## Overview

HTMX extends HTML with attributes that enable AJAX requests, CSS transitions, WebSockets, and Server-Sent Events directly in markup. Responses are HTML fragments, not JSON.

Core principle: Server returns HTML, HTMX swaps it into the DOM.

---

## Core Request Attributes

### HTTP Methods

    hx-get="/url"         -- GET request
    hx-post="/url"        -- POST request
    hx-put="/url"         -- PUT request
    hx-patch="/url"       -- PATCH request
    hx-delete="/url"      -- DELETE request

### Basic Example

    <button hx-get="/api/data" hx-target="#result">
      Load
    </button>
    <div id="result"></div>

---

## Targeting (hx-target)

### Syntax

    hx-target="<CSS selector>"
    hx-target="this"              -- the element itself
    hx-target="closest <selector>" -- closest ancestor matching selector
    hx-target="find <selector>"    -- first descendant matching selector
    hx-target="next"              -- next sibling
    hx-target="next <selector>"   -- next sibling matching selector
    hx-target="previous"          -- previous sibling
    hx-target="previous <selector>" -- previous sibling matching selector

### Examples

    hx-target="#results"          -- element with id="results"
    hx-target=".output"           -- first element with class="output"
    hx-target="this"              -- replace triggering element
    hx-target="closest tr"        -- closest ancestor <tr>
    hx-target="closest .card"     -- closest ancestor with class="card"
    hx-target="find .content"     -- first .content descendant
    hx-target="next"              -- next sibling element
    hx-target="next .message"     -- next sibling with class="message"
    hx-target="previous input"    -- previous sibling <input>

### Default Behavior

    If no hx-target specified, the triggering element is the target.

---

## Swap Strategies (hx-swap)

### Syntax

    hx-swap="<strategy> [modifiers]"

### Strategies

    innerHTML     -- replace inner content of target (DEFAULT)
    outerHTML     -- replace entire target element
    beforebegin   -- insert before target element
    afterbegin    -- insert at start of target's content
    beforeend     -- insert at end of target's content
    afterend      -- insert after target element
    delete        -- delete target element (ignores response)
    none          -- no swap (just trigger events)

### Visual Reference

    Given: <div id="target">old content</div>
    Response: <p>new</p>

    innerHTML:    <div id="target"><p>new</p></div>
    outerHTML:    <p>new</p>  (div is gone)
    beforebegin:  <p>new</p><div id="target">old content</div>
    afterbegin:   <div id="target"><p>new</p>old content</div>
    beforeend:    <div id="target">old content<p>new</p></div>
    afterend:     <div id="target">old content</div><p>new</p>
    delete:       (element removed, response ignored)
    none:         <div id="target">old content</div> (unchanged)

### Swap Modifiers

    swap:<time>       -- delay before swap (default: 0ms)
    settle:<time>     -- delay before settle (default: 20ms)
    show:<target>     -- scroll to element after swap
    scroll:<target>   -- scroll target element
    focus-scroll:true -- preserve focus scroll position
    transition:true   -- use View Transitions API

### Modifier Examples

    hx-swap="innerHTML swap:500ms"        -- wait 500ms before swapping
    hx-swap="innerHTML settle:100ms"      -- wait 100ms for settle
    hx-swap="innerHTML show:top"          -- scroll to top after swap
    hx-swap="innerHTML show:#element:top" -- scroll element to top
    hx-swap="innerHTML scroll:top"        -- scroll target to top
    hx-swap="innerHTML scroll:bottom"     -- scroll target to bottom
    hx-swap="innerHTML transition:true"   -- use View Transitions

### Timing Explanation

    1. Response received
    2. swap: delay (default 0ms)
    3. Old content removed, new content inserted
    4. htmx:afterSwap fires
    5. settle: delay (default 20ms) -- allows CSS transitions to start
    6. htmx:afterSettle fires

---

## Out-of-Band Swaps (hx-swap-oob)

### Purpose

Update multiple parts of the page from a single response.

### Server Response Format

    <!-- Main response (swapped into hx-target) -->
    <div>Main content</div>

    <!-- OOB elements (swapped by their own id) -->
    <div id="notification" hx-swap-oob="true">New notification!</div>
    <div id="counter" hx-swap-oob="true">42</div>

### OOB Swap Strategies

    hx-swap-oob="true"            -- outerHTML (replace element with same id)
    hx-swap-oob="outerHTML"       -- same as true
    hx-swap-oob="innerHTML"       -- replace inner content only
    hx-swap-oob="beforebegin"     -- insert before target
    hx-swap-oob="afterbegin"      -- insert at start of target
    hx-swap-oob="beforeend"       -- append to target
    hx-swap-oob="afterend"        -- insert after target
    hx-swap-oob="delete"          -- remove target element
    hx-swap-oob="none"            -- no swap

### Targeting Specific Element

    hx-swap-oob="innerHTML:#target-id"
    hx-swap-oob="beforeend:#list"

### Common OOB Patterns

Update notification badge after action:
Response:

<div>Action completed</div>
<span id="badge" hx-swap-oob="true">5</span>

Append to list:
Response:

<tr id="new-row">...</tr> <!-- main response -->
<tbody id="table-body" hx-swap-oob="beforeend">
<tr>New row appended</tr>
</tbody>

Clear form after submit:
Response:

<div id="result">Saved!</div>
<form id="my-form" hx-swap-oob="outerHTML">
<!-- fresh empty form -->
</form>

### OOB Gotchas

1. OOB element MUST have an id attribute
2. Target element with matching id MUST exist in DOM
3. OOB elements are processed AFTER main swap
4. OOB element is removed from response before main swap
5. Use hx-swap-oob="innerHTML" to preserve element attributes/handlers

DON'T - OOB without id:

<div hx-swap-oob="true">No id, won't work</div>

DO - Include id:

<div id="target" hx-swap-oob="true">Has id, works</div>

DON'T - Target doesn't exist:
Response: <div id="nonexistent" hx-swap-oob="true">...</div>
DOM: (no element with id="nonexistent")
Result: OOB ignored silently

DO - Ensure target exists:
Response: <div id="status" hx-swap-oob="true">...</div>
DOM: <div id="status">old</div>
Result: Works

---

## Triggers (hx-trigger)

### Syntax

    hx-trigger="<event> [filters] [modifiers]"

### Standard Events

    hx-trigger="click"
    hx-trigger="change"
    hx-trigger="submit"
    hx-trigger="keyup"
    hx-trigger="mouseenter"
    hx-trigger="mouseleave"
    hx-trigger="focus"
    hx-trigger="blur"
    hx-trigger="input"
    hx-trigger="load"             -- fires on page load
    hx-trigger="revealed"         -- fires when element scrolls into view
    hx-trigger="intersect"        -- fires when element intersects viewport

### Custom Events

    hx-trigger="my-custom-event"
    hx-trigger="htmx:afterSettle"

### Multiple Triggers

    hx-trigger="click, keyup[key=='Enter']"

### Event Filters

    hx-trigger="click[ctrlKey]"           -- only with ctrl held
    hx-trigger="click[shiftKey]"          -- only with shift held
    hx-trigger="keyup[key=='Enter']"      -- only Enter key
    hx-trigger="keyup[key=='Escape']"     -- only Escape key
    hx-trigger="click[target.id=='foo']"  -- only if target has id="foo"
    hx-trigger="submit[checkValidity()]"  -- only if form is valid

### Modifiers

    once          -- trigger only once
    changed       -- only if value changed
    delay:<time>  -- wait before triggering (resets on new event)
    throttle:<time> -- at most once per time period
    from:<selector> -- listen on different element
    target:<selector> -- filter by event target
    consume       -- stop event propagation
    queue:<mode>  -- queue behavior (first, last, all, none)

### Modifier Examples

    hx-trigger="click once"                    -- only first click
    hx-trigger="input changed"                 -- only if value changed
    hx-trigger="input changed delay:500ms"     -- debounce 500ms
    hx-trigger="scroll throttle:200ms"         -- at most every 200ms
    hx-trigger="click from:body"               -- listen on body
    hx-trigger="click from:closest .container" -- listen on ancestor
    hx-trigger="click from:#other-element"     -- listen on specific element
    hx-trigger="click target:.child"           -- only if clicked .child
    hx-trigger="click consume"                 -- stopPropagation
    hx-trigger="click queue:first"             -- ignore while request pending
    hx-trigger="click queue:last"              -- replace queued request
    hx-trigger="click queue:all"               -- queue all requests
    hx-trigger="click queue:none"              -- drop if request pending

### Polling

    hx-trigger="every 2s"                      -- poll every 2 seconds
    hx-trigger="every 5s [isActive]"           -- conditional polling

### Intersection Observer

    hx-trigger="intersect"                           -- any intersection
    hx-trigger="intersect once"                      -- load when visible (once)
    hx-trigger="intersect threshold:0.5"             -- 50% visible
    hx-trigger="intersect root:.container"           -- relative to container
    hx-trigger="intersect rootMargin:100px"          -- margin around root

### Default Triggers

    <input>, <textarea>, <select>  -- "change"
    <form>                          -- "submit"
    everything else                 -- "click"

---

## Request Data

### Including Values

    hx-vals='{"key": "value"}'              -- JSON object
    hx-vals='js:{key: computeValue()}'      -- JavaScript expression

### Including Other Inputs

    hx-include="[name='csrf']"              -- include by selector
    hx-include="closest form"               -- include all inputs in form
    hx-include="this"                       -- include this element's value
    hx-include="#other-form"                -- include another form's inputs

### Parameters from Attributes

    hx-params="*"                           -- all parameters (default)
    hx-params="none"                        -- no parameters
    hx-params="not name1, name2"            -- exclude specific
    hx-params="name1, name2"                -- only specific

### Headers

    hx-headers='{"X-Custom": "value"}'
    hx-headers='js:{"X-Timestamp": Date.now()}'

---

## Response Headers (Server-Side)

### Redirect

    HX-Redirect: /new-url              -- client-side redirect
    HX-Location: /new-url              -- like HX-Redirect but more control
    HX-Location: {"path": "/new", "target": "#content"}

### Refresh

    HX-Refresh: true                   -- full page refresh

### Swap Control

    HX-Reswap: innerHTML               -- override hx-swap
    HX-Retarget: #other                -- override hx-target
    HX-Reselect: .content              -- select portion of response

### Push URL

    HX-Push-Url: /new-url              -- push to history
    HX-Push-Url: false                 -- prevent push
    HX-Replace-Url: /new-url           -- replace in history

### Triggers

    HX-Trigger: myEvent                -- trigger event after swap
    HX-Trigger: {"myEvent": {"key": "value"}}  -- with detail
    HX-Trigger-After-Settle: myEvent   -- trigger after settle
    HX-Trigger-After-Swap: myEvent     -- trigger after swap

---

## History and URLs

### Push URL

    hx-push-url="true"                 -- push current request URL
    hx-push-url="/custom-url"          -- push custom URL
    hx-push-url="false"                -- don't push (default)

### Replace URL

    hx-replace-url="true"              -- replace with request URL
    hx-replace-url="/custom-url"       -- replace with custom URL

### History Restoration

    When back button pressed, HTMX restores from cache or re-fetches.
    Element with id="main" or hx-history-elt attribute is snapshot target.

    <div id="main" hx-history-elt>
      <!-- this content is cached/restored -->
    </div>

---

## Indicators

### Basic Loading Indicator

    <button hx-get="/slow" hx-indicator="#spinner">
      Load
    </button>
    <span id="spinner" class="htmx-indicator">Loading...</span>

### CSS for Indicators

    .htmx-indicator {
      opacity: 0;
      transition: opacity 200ms ease-in;
    }
    .htmx-request .htmx-indicator {
      opacity: 1;
    }
    .htmx-request.htmx-indicator {
      opacity: 1;
    }

### Indicator on Self

    <button hx-get="/slow" hx-indicator="this">
      <span class="htmx-indicator">...</span>
      Load
    </button>

### Disable During Request

    <button hx-get="/api" hx-disabled-elt="this">
      Submit
    </button>

    hx-disabled-elt="this"             -- disable self
    hx-disabled-elt="closest button"   -- disable ancestor
    hx-disabled-elt="#submit-btn"      -- disable specific element

---

## Boosting

### Link Boosting

    <a href="/page" hx-boost="true">Link</a>

    Converts to AJAX request, swaps body content.

### Form Boosting

    <form action="/submit" method="post" hx-boost="true">
      <!-- form submits via AJAX -->
    </form>

### Boost Inheritance

    <div hx-boost="true">
      <a href="/a">Boosted</a>
      <a href="/b">Also boosted</a>
      <a href="/c" hx-boost="false">Not boosted</a>
    </div>

---

## Inheritance

### Attribute Inheritance

HTMX attributes inherit down the DOM tree:

<div hx-target="#result" hx-swap="outerHTML">
<button hx-get="/a">Uses parent target/swap</button>
<button hx-get="/b">Also uses parent target/swap</button>
<button hx-get="/c" hx-target="#other">Overrides target</button>
</div>

### Disable Inheritance

    hx-disinherit="*"                  -- disable all inheritance
    hx-disinherit="hx-target"          -- disable specific attribute
    hx-disinherit="hx-target hx-swap"  -- disable multiple

---

## Confirmation and Prompts

### Confirm

    <button hx-delete="/item" hx-confirm="Are you sure?">Delete</button>

### Prompt

    Use htmx:configRequest event to add prompt value:
    <button hx-post="/rename"
            _="on htmx:configRequest
               set name to prompt('New name?')
               if name is null halt the event end
               set event.detail.parameters.name to name">
      Rename
    </button>

---

## Validation

### Form Validation

    <form hx-post="/submit">
      <input name="email" type="email" required>
      <button type="submit">Submit</button>
    </form>

    HTMX respects HTML5 validation. Form won't submit if invalid.

### Disable Validation

    <form hx-post="/submit" novalidate>

### Validation Extension

    <script src="https://unpkg.com/htmx.org/dist/ext/validation.js"></script>
    <form hx-ext="validation" hx-post="/submit">

---

## Events Reference

### Request Lifecycle

    htmx:configRequest     -- configure request (modify headers, params)
    htmx:beforeRequest     -- before request sent (can cancel)
    htmx:beforeSend        -- XHR about to be sent
    htmx:afterRequest      -- after response received (success or error)
    htmx:responseError     -- HTTP error response (4xx, 5xx)
    htmx:sendError         -- network error

### Swap Lifecycle

    htmx:beforeSwap        -- before swap (can modify/cancel)
    htmx:afterSwap         -- after DOM updated
    htmx:afterSettle       -- after settle delay (transitions done)
    htmx:load              -- fired on new content

### Other Events

    htmx:abort             -- request aborted
    htmx:beforeOnLoad      -- before load handler
    htmx:beforeProcessNode -- before processing element
    htmx:afterProcessNode  -- after processing element
    htmx:historyCacheError -- history cache error
    htmx:historyRestore    -- history restoration
    htmx:beforeHistorySave -- before saving to history
    htmx:pushedIntoHistory -- after pushing to history
    htmx:oobBeforeSwap     -- before OOB swap
    htmx:oobAfterSwap      -- after OOB swap
    htmx:prompt            -- confirm prompt shown
    htmx:timeout           -- request timeout
    htmx:validation:validate -- validation check
    htmx:validation:failed -- validation failed
    htmx:xhr:loadend       -- XHR loadend
    htmx:xhr:loadstart     -- XHR loadstart
    htmx:xhr:progress      -- XHR progress

### Event Detail Properties

    event.detail.elt              -- element that triggered
    event.detail.target           -- swap target element
    event.detail.requestConfig    -- request configuration
    event.detail.xhr              -- XMLHttpRequest object
    event.detail.successful       -- true if 2xx response
    event.detail.failed           -- true if error
    event.detail.pathInfo         -- URL path info
    event.detail.parameters       -- request parameters

---

## Configuration

### Global Config

    htmx.config.historyEnabled = true
    htmx.config.historyCacheSize = 10
    htmx.config.refreshOnHistoryMiss = false
    htmx.config.defaultSwapStyle = "innerHTML"
    htmx.config.defaultSwapDelay = 0
    htmx.config.defaultSettleDelay = 20
    htmx.config.includeIndicatorStyles = true
    htmx.config.indicatorClass = "htmx-indicator"
    htmx.config.requestClass = "htmx-request"
    htmx.config.addedClass = "htmx-added"
    htmx.config.settlingClass = "htmx-settling"
    htmx.config.swappingClass = "htmx-swapping"
    htmx.config.allowEval = true
    htmx.config.useTemplateFragments = false
    htmx.config.wsReconnectDelay = "full-jitter"
    htmx.config.disableSelector = "[hx-disable], [data-hx-disable]"
    htmx.config.timeout = 0

### Meta Tag Config

    <meta name="htmx-config" content='{"defaultSwapStyle": "outerHTML"}'>

---

## Extensions

### Loading Extensions

    <script src="https://unpkg.com/htmx.org/dist/ext/json-enc.js"></script>

    <div hx-ext="json-enc">
      <form hx-post="/api">  <!-- sends JSON instead of form data -->
    </div>

### Common Extensions

    json-enc        -- send JSON body
    client-side-templates  -- mustache/handlebars support
    class-tools     -- add/remove classes on events
    loading-states  -- loading state management
    morphdom-swap   -- use morphdom for smarter diffing
    alpine-morph    -- use Alpine's morph
    preload         -- preload links on hover
    path-deps       -- declare path dependencies
    multi-swap      -- multiple swap targets
    response-targets -- target by response code
    restored        -- detect restored elements

### Response Targets Extension

    <script src="https://unpkg.com/htmx.org/dist/ext/response-targets.js"></script>

    <div hx-ext="response-targets">
      <form hx-post="/submit"
            hx-target="#success"
            hx-target-4xx="#error"
            hx-target-500="#server-error">
      </form>
    </div>

---

## Common Patterns

### Infinite Scroll

    <div id="items">
      <div class="item">...</div>
      <div class="item">...</div>
      <div id="load-more"
           hx-get="/items?page=2"
           hx-trigger="revealed"
           hx-swap="outerHTML">
        Loading...
      </div>
    </div>

    Server returns more items + new load-more with page=3.

### Active Search

    <input type="search"
           name="q"
           hx-get="/search"
           hx-trigger="input changed delay:300ms"
           hx-target="#results"
           hx-indicator="#spinner">

### Delete Row

    <tr>
      <td>Item</td>
      <td>
        <button hx-delete="/items/123"
                hx-target="closest tr"
                hx-swap="outerHTML swap:500ms"
                hx-confirm="Delete this item?">
          Delete
        </button>
      </td>
    </tr>

### Edit in Place

    <div hx-get="/edit/123" hx-trigger="click" hx-swap="outerHTML">
      Click to edit
    </div>

    Server returns form:
    <form hx-put="/items/123" hx-swap="outerHTML">
      <input name="value" value="Click to edit">
      <button type="submit">Save</button>
      <button hx-get="/items/123" hx-swap="outerHTML">Cancel</button>
    </form>

### Tabs

    <div class="tabs">
      <button hx-get="/tab1" hx-target="#tab-content" class="active">Tab 1</button>
      <button hx-get="/tab2" hx-target="#tab-content">Tab 2</button>
      <button hx-get="/tab3" hx-target="#tab-content">Tab 3</button>
    </div>
    <div id="tab-content">
      <!-- content -->
    </div>

### Modal Dialog

    <button hx-get="/modal/edit" hx-target="#modal-container">Edit</button>

    <div id="modal-container"></div>

    Server returns:
    <div class="modal-backdrop">
      <div class="modal">
        <form hx-put="/items/123" hx-target="#modal-container" hx-swap="innerHTML">
          ...
          <button type="submit">Save</button>
          <button hx-get="/empty" hx-target="#modal-container">Cancel</button>
        </form>
      </div>
    </div>

### Cascading Selects

    <select name="country"
            hx-get="/states"
            hx-target="#state-select"
            hx-trigger="change">
      <option value="us">United States</option>
      <option value="ca">Canada</option>
    </select>

    <select id="state-select" name="state">
      <option>Select country first</option>
    </select>

### Progress Bar

    <div hx-get="/job/status"
         hx-trigger="every 1s"
         hx-target="this"
         hx-swap="innerHTML">
      <div class="progress" style="width: 0%"></div>
    </div>

    Server returns updated progress until complete:
    <div class="progress" style="width: 50%"></div>

    Final response (no hx-get, stops polling):
    <div class="complete">Done!</div>

### Bulk Operations

    <form hx-post="/bulk-delete" hx-target="#table-body">
      <table>
        <thead>
          <tr>
            <th><input type="checkbox" onclick="toggleAll(this)"></th>
            <th>Name</th>
          </tr>
        </thead>
        <tbody id="table-body">
          <tr>
            <td><input type="checkbox" name="ids" value="1"></td>
            <td>Item 1</td>
          </tr>
        </tbody>
      </table>
      <button type="submit">Delete Selected</button>
    </form>

---

## Anti-Patterns

### Missing Target

DON'T - No target, replaces button:
<button hx-get="/content">Load</button>

DO - Specify target:
<button hx-get="/content" hx-target="#result">Load</button>

### Wrong Swap for Replace

DON'T - innerHTML when you want to replace:

<div id="item" hx-get="/item/123" hx-swap="innerHTML">
Response: <div id="item">new content</div>
Result: <div id="item"><div id="item">new content</div></div>

DO - Use outerHTML for full replacement:

<div id="item" hx-get="/item/123" hx-swap="outerHTML">

### OOB Without ID

DON'T - Missing id attribute:
Response: <div hx-swap-oob="true">content</div>
Result: Silently ignored

DO - Always include id:
Response: <div id="target" hx-swap-oob="true">content</div>

### OOB Target Missing

DON'T - Target doesn't exist in DOM:
Response: <div id="nonexistent" hx-swap-oob="true">...</div>
Result: Silently ignored

DO - Ensure target exists before OOB:
Have placeholder: <div id="notifications"></div>

### Polling Without Stop Condition

DON'T - Infinite polling:

<div hx-get="/status" hx-trigger="every 1s">

DO - Stop when complete (server removes hx-trigger):

<div hx-get="/status" hx-trigger="every 1s">
Final response: <div>Complete</div> (no hx-get or hx-trigger)

### Nested hx-boost

DON'T - Confusing nested boost:

<div hx-boost="true">
<div hx-boost="true">
<a href="/page">Link</a>

DO - Single boost at appropriate level:

<div hx-boost="true">
<a href="/page">Link</a>

### Duplicate IDs

DON'T - Response creates duplicate IDs:
DOM: <div id="item">old</div>
Response appended: <div id="item">new</div>
Result: Two elements with same ID

DO - Use unique IDs or outerHTML to replace:
hx-swap="outerHTML"
or
Generate unique IDs: id="item-123"

### Wrong Event for Inputs

DON'T - click on input:
<input hx-get="/search" hx-trigger="click">

DO - Use input or change:
<input hx-get="/search" hx-trigger="input changed delay:300ms">

### Forgetting hx-include

DON'T - Value not sent:
<input id="search" name="q">
<button hx-get="/search">Search</button>

DO - Include the input:
<input id="search" name="q">
<button hx-get="/search" hx-include="#search">Search</button>

    Or use a form:
    <form hx-get="/search">
      <input name="q">
      <button>Search</button>
    </form>

---

## Debugging

### Enable Debug Logging

    htmx.logAll();

### Disable Logging

    htmx.logNone();

### Inspect Element Config

    htmx.closest(element, selector)  -- find closest ancestor
    htmx.find(selector)              -- find element
    htmx.findAll(selector)           -- find all elements
    htmx.values(element)             -- get form values

### Console Debugging

    document.body.addEventListener('htmx:configRequest', function(evt) {
      console.log('Request:', evt.detail);
    });

    document.body.addEventListener('htmx:afterRequest', function(evt) {
      console.log('Response:', evt.detail.xhr.response);
    });

    document.body.addEventListener('htmx:swapError', function(evt) {
      console.error('Swap error:', evt.detail);
    });

### Common Debug Events

    htmx:beforeRequest   -- see what's being sent
    htmx:afterRequest    -- see response
    htmx:beforeSwap      -- see what's being swapped
    htmx:afterSwap       -- confirm swap completed
    htmx:swapError       -- catch swap failures
    htmx:targetError     -- target not found

### Network Tab

Check browser Network tab for: - Request URL and method - Request headers (HX-Request, HX-Target, etc.) - Response status and body - Response headers (HX-Trigger, HX-Reswap, etc.)

### HTMX Request Headers (sent automatically)

    HX-Request: true
    HX-Target: <target element id>
    HX-Trigger: <triggering element id>
    HX-Trigger-Name: <triggering element name>
    HX-Current-URL: <current page URL>
    HX-Prompt: <user prompt response>
    HX-Boosted: true (if boosted)
    HX-History-Restore-Request: true (if history restore)

---

## Server Response Checklist

For each HTMX endpoint, verify:

1. Response is HTML fragment (not full page, not JSON)
2. OOB elements have id attributes
3. OOB targets exist in DOM
4. IDs in response don't duplicate existing IDs (unless replacing)
5. Response matches expected hx-swap strategy
6. HX-Trigger headers fire at right time
7. Status code is appropriate (200 for success, 4xx/5xx for errors)

---

## Go Template Integration

### Partial Templates

    // Handler
    func handlePartial(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("HX-Request") == "true" {
            tmpl.ExecuteTemplate(w, "partial.html", data)
        } else {
            tmpl.ExecuteTemplate(w, "full-page.html", data)
        }
    }

### OOB in Templates

    {{ define "item-with-oob" }}
    <div id="item-{{ .ID }}">{{ .Content }}</div>
    <div id="notification" hx-swap-oob="true">Item updated!</div>
    {{ end }}

### Response Headers in Go

    func handler(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("HX-Trigger", "itemUpdated")
        w.Header().Set("HX-Push-Url", "/items/123")
        // ...
    }

### Redirect After POST

    func handlePost(w http.ResponseWriter, r *http.Request) {
        // Process form...
        if r.Header.Get("HX-Request") == "true" {
            w.Header().Set("HX-Redirect", "/success")
            w.WriteHeader(200)
        } else {
            http.Redirect(w, r, "/success", http.StatusSeeOther)
        }
    }

---

## HTMX + Hyperscript Integration

### Respond to HTMX Events

    <div _="on htmx:afterSwap call initComponent(event.detail.elt)">

### Trigger HTMX from Hyperscript

    <button _="on click htmx.trigger(#form, 'submit')">Submit</button>

### Pre-request Validation

    <form hx-post="/submit"
          _="on htmx:configRequest
               if #email.value is empty
                 halt the event
                 add .error to #email
               end">

### Post-request Cleanup

    <button hx-post="/save"
            _="on htmx:afterRequest from me
                 remove .loading from me
                 if event.detail.successful
                   add .success to me
                 else
                   add .error to me
                 end">

### Coordinate Multiple Elements

    <button hx-get="/content" hx-target="#main"
            _="on htmx:beforeRequest
                 add .loading to #sidebar
               on htmx:afterRequest
                 remove .loading from #sidebar">
