# Web Interface Refactoring Plan

## Overview

Refactor the sfpg-go web interface to be fully responsive and minimalist, using DaisyUI components where appropriate, TailwindCSS for styling, and Hyperscript for interactivity.

## User Preferences (Confirmed)

1. **Info Box**: Slide-out drawer from the LEFT edge
2. **Mobile Footer**: If lightbox is swipeable, exclude Prev/Next buttons. Keep all others.
3. **Button Style**: Keep current style (DaisyUI buttons with additional styling - already fits on iPhone)
4. **Gallery Tiles**: Keep consistent size as currently implemented. Thumbnails fit within box with varying aspect ratios.
5. **Layout**: Use Flexbox instead of Grid for better scaling on high-resolution screens
6. **Testing**: Use Playwright to visually verify before finalizing

## Current Issues

### 1. Fixed Dimensions Throughout

- Gallery tiles: fixed `h-[202px] w-[238px]`
- Info box: fixed `w-[236px]`
- Footer: fixed `h-[72px]`
- Thumbnail containers: fixed dimensions

### 2. Non-Responsive Layout

- Gallery uses `inline-block` - should use Flexbox for better scaling
- Info box does not collapse on mobile
- Fixed pixel margins and padding

### 3. Mixed Styling Approaches

- Some elements use DaisyUI, others use raw Tailwind
- Inline `style=` attributes for transforms
- Custom CSS classes that duplicate DaisyUI functionality

---

## Summarized List of Changes

### Phase 1: Gallery Flexbox Refactor ✅ IMPLEMENTED

- ✅ Switch to `flex flex-wrap` for better scaling on all screen sizes
- ✅ Tiles maintain consistent size (current dimensions)
- ✅ Thumbnails fit within tile with varying aspect ratios (`object-contain` in `<figure>`)
- ✅ Use DaisyUI `card` component for tiles
- ✅ Center-ellipsis truncation for long file/directory names

### Phase 2: Footer/Toolbar Refactor ✅ IMPLEMENTED

- ✅ Keep current button styling (DaisyUI with additional custom styling)
- ✅ Add swipe gestures to lightbox for mobile navigation (Hyperscript pointerdown/pointerup, 48px threshold)
- ✅ Hide Prev/Next buttons on mobile when swipe is available (`hidden sm:flex`)
- ✅ Keep all other buttons visible

### Phase 3: Info Box Refactor ✅ IMPLEMENTED (modal pattern, not drawer)

- ✅ Info box hidden on mobile; replaced with dedicated `#mobile_info_modal` (daisyUI checkbox modal)
- ✅ CSS media query (`hover:none` + `pointer:coarse`) toggles between mobile/desktop info buttons
- ✅ Content mirrored from `#box_info` to modal on every HTMX swap (no extra request)
- ⚠️ Drawer-from-left pattern was not used; modal pattern chosen for better iOS compatibility

### Phase 4: Lightbox Refactor ✅ IMPLEMENTED

- ✅ Swipe gesture support (pointerdown/pointerup, 48px threshold, ±40px drift guard)
- ✅ Ghost tap prevention via `:didSwipe` flag
- ✅ Modal height uses `100dvh` + `env(safe-area-inset-bottom)` for iOS Safari
- ✅ Responsive controls layout

### Phase 5: Dashboard Refactor ✅ IMPLEMENTED

- ✅ Compact typography scale (headings scaled down, `font-mono` removed from stats)
- ✅ `stat-title` elements get `text-xs` for better density
- ✅ `stat-value` elements use `font-semibold` instead of `font-mono`

### Phase 6: Modal Consistency ✅ IMPLEMENTED

- ✅ Mobile info modal uses daisyUI `modal` + checkbox pattern
- ✅ `env(safe-area-inset-bottom)` applied to body and modals
- ✅ `htmx-indicator` given `hidden` class by default to prevent flash on load

---

## Detailed Implementation Plan

### File: `web/templates/gallery.html.tmpl`

**Current:**

```html
<div class="gallery-tile inline-block h-[202px] w-[238px] content-start"></div>
```

**Proposed:**

- Use `flex flex-wrap` container for better scaling
- Keep consistent tile size (h-[188px] w-[224px])
- Use DaisyUI `card` component for cleaner markup
- Thumbnails fit within tile with `object-contain` for varying aspect ratios

```html
<div id="boxgallery" class="flex flex-wrap justify-center gap-[14px] p-[7px]">
  <div
    class="card bg-base-200 border-base-300 h-[188px] w-[224px] border shadow-md transition-shadow hover:shadow-xl"
  >
    <figure class="h-[168px] p-2">
      <img src="..." class="h-full w-full rounded object-contain" />
    </figure>
    <div class="card-body truncate p-1 text-center text-xs">
      {{ .DispName }}
    </div>
  </div>
</div>
```

### File: `web/templates/layout.html.tmpl`

**Info Box - Current:**

```html
<div
  id="box_info_wrapper"
  class="relative mt-[7px] mb-[7px] ml-[7px] hidden h-[calc(100vh-72px-14px)] w-[236px] flex-shrink-0"
></div>
```

**Proposed:**

- Slide-out drawer from LEFT edge
- Use DaisyUI `drawer` component pattern
- Visible on desktop (lg:drawer-open), hidden on mobile with toggle button

```html
<div class="drawer drawer-start lg:drawer-open">
  <input id="info-drawer" type="checkbox" class="drawer-toggle" />
  <div class="drawer-content flex flex-col">
    <!-- Main content area -->
    <div id="BodyContainer" class="relative flex-1">
      <!-- Gallery content -->
    </div>
    <!-- Footer -->
  </div>
  <div class="drawer-side z-40">
    <label for="info-drawer" class="drawer-overlay"></label>
    <div class="card bg-base-200 h-full w-80 p-4">
      <!-- Info content -->
    </div>
  </div>
</div>
```

**Footer Buttons:**

- Keep current styling (DaisyUI buttons with additional custom styling)
- Add swipe gesture support to lightbox
- Hide Prev/Next buttons on mobile when swipe is available using `hidden sm:flex`

### File: `web/templates/lightbox-content.html.tmpl`

**Swipe Gesture Addition:**

Add Hyperscript swipe handlers to the lightbox container:

```hyperscript
on swipeleft
  if #lightbox-next-btn is not disabled
    call lightboxNav('/raw-image/{{ .NextIndex }}', '{{ .NextIndex }}')
  end
end

on swiperight
  if #lightbox-prev-btn is not disabled
    call lightboxNav('/raw-image/{{ .PrevIndex }}', '{{ .PrevIndex }}')
  end
end
```

**Responsive Button Visibility:**

```html
<!-- Hide Prev/Next on mobile when swipe is available -->
<button id="lightbox-prev-btn" class="hidden sm:flex ...">
  <button id="lightbox-next-btn" class="hidden sm:flex ..."></button>
</button>
```

### File: `web/templates/infobox-image.html.tmpl`

**Current:** Fixed width container with custom spacing

**Proposed:**

- Use DaisyUI `card` component
- Use `stats` for dimensions display
- Cleaner typography with proper spacing

```html
<div class="card bg-base-100 border-base-300 border">
  <figure class="p-2">
    <img src="/thumbnail/file/{{ .File.ID }}" class="rounded" />
  </figure>
  <div class="card-body gap-2 text-xs">
    <h3 class="card-title text-sm">{{ .File.Filename }}</h3>
    <div class="stats stats-sm shadow">
      <div class="stat">
        <div class="stat-title">Size</div>
        <div class="stat-value text-sm">
          {{ .File.Width }}x{{ .File.Height }}
        </div>
      </div>
    </div>
  </div>
</div>
```

---

## Responsive Breakpoints Strategy

| Breakpoint       | Gallery Layout      | Info Box           | Footer Controls              |
| ---------------- | ------------------- | ------------------ | ---------------------------- |
| Default (mobile) | Flex wrap, centered | Hidden, drawer tog | All except Prev/Next (swipe) |
| `sm` (640px)     | Flex wrap, centered | Hidden, drawer tog | All controls visible         |
| `lg` (1024px)    | Flex wrap, centered | Visible sidebar    | All controls visible         |

---

## Components to Use (DaisyUI)

1. **`card`** - Gallery tiles, info boxes, dashboard cards
2. **`modal`** - Lightbox, login, theme selector, config
3. **`drawer`** - Info box (slide from left)
4. **`stats`** - Dashboard metrics, image info
5. **`menu`** - Hamburger dropdown
6. **`breadcrumbs`** - Already using, keep
7. **`badge`** - Status indicators
8. **`alert`** - Error messages, notifications

---

## Files to Modify

1. `web/templates/layout.html.tmpl` - Main layout, footer, info box drawer
2. `web/templates/gallery.html.tmpl` - Gallery flex layout
3. `web/templates/lightbox-content.html.tmpl` - Swipe gestures, responsive buttons
4. `web/templates/infobox-image.html.tmpl` - Card component, stats
5. `web/templates/infobox-folder.html.tmpl` - Card component, stats
6. `web/templates/dashboard.html.tmpl` - Minor responsive fixes

---

## Hyperscript Considerations

- Keep existing keyboard handler in layout.html.tmpl
- Add swipe gesture handlers to lightbox
- Keep info box toggle logic (now triggers drawer)
- Update selectors if DOM structure changes
- No new JavaScript needed

---

## Testing Strategy

1. Use Playwright for visual testing
2. Test on mobile viewport (320px - 640px)
3. Test on tablet viewport (768px - 1024px)
4. Test on desktop viewport (1280px+)
5. Test on high-resolution screens (4K)
6. Verify all HTMX interactions still work
7. Verify keyboard navigation still works
8. Test with `air` dev server on port 8083

---

## Implementation Order

1. **Gallery** - Flex layout refactor
2. **Info Box** - Drawer implementation
3. **Lightbox** - Swipe gestures + responsive buttons
4. **Info Panels** - Card component refactor
5. **Dashboard** - Minor adjustments
6. **Visual Testing** - Playwright verification
