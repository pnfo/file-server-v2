# Design Document & Architecture Review: Astro File Server Migration

> Historical: written for an earlier Astro + Vue rewrite in `astro-server/` of the old pnfo/file-server-new repo; its file paths refer to that code, not this repo. The UI parity rules (concerns 1–12, §9) and the download pitfalls (§10) still apply; [design.md](design.md) supersedes the rest.

**Project:** Tipitaka & Buddhist Media File Server (`file-server`)  
**Target Migration:** Astro SSR + Tailwind CSS + SQLite + Vue 3 Admin Islands  
**Document Name:** `design-concerns.md`  
**Purpose:** Comprehensive architectural record of user requirements, UI/UX parity concerns, edge-case debugging, schema evolution, and lessons learned for handover to other AI coding models and engineering teams.

---

## 1. Project Introduction & Business Goals

### 1.1 Context
The `file-server` is a high-traffic production web service serving thousands of Buddhist texts, Tipitaka translations, audio sermons (Dhamma Desana), ancient scanned manuscripts, dictionary databases, 3D models, and offline software for desktop and Android. The original application was built with **Nuxt 3** and **Vuetify 3**, querying AWS S3-compatible DigitalOcean Spaces storage and JSON metadata files.

### 1.2 Motivation for Refactoring
While the Nuxt 3 / Vuetify implementation was functional, it suffered from:
1. **Excessive Client Bundle Size:** Vuetify's extensive component suite loaded megabytes of JavaScript and CSS on initial visit, hurting mobile load times and Core Web Vitals.
2. **SEO & Web Crawler Latency:** Search crawlers and social media preview bots often received empty mounting shells or had to wait for heavy client-side hydration.
3. **Complex State Synchronization:** Tree navigation and metadata synchronization across multiple JSON store files caused maintenance friction.
4. **Lack of Admin Capabilities:** Uploading files, editing metadata, organizing folders, selecting PDF preview pages, and unpublishing files required manual S3 manipulation or script execution.

### 1.3 Key Architectural Goals
- **Isolated Migration in `astro-server/`:** Build the entire refactored platform in an isolated subdirectory (`/Volumes/2TB/node/file-server/astro-server/`) so the existing production application runs untouched until full parity is verified.
- **Pure SSR First:** Leverage **Astro (Node SSR Adapter)** to render 100% pre-rendered, server-side semantic HTML for instant first contentful paint (FCP), zero layout shift, and perfect SEO indexing.
- **Embedded Fast SQLite Database:** Replace JSON mapping files with an embedded SQLite database using **better-sqlite3** and **Drizzle ORM**, providing sub-millisecond query times and indexed search.
- **Selective Hydration (Vue 3 Islands):** Use Vue 3 only where client-side interactivity is genuinely required (the searchable/sortable `EntryListTable.vue` and the Admin Dashboard modals).
- **Exact Visual & Behavioral Parity:** Match the look and feel, responsive behavior, colors, typographic hierarchy, and Sinhala phonetic search behavior of the original Vuetify interface.
- **Multi-Profile & Multi-Port Support:** Drive different deployments (`PROFILE=library`, `PROFILE=cloud`, `PROFILE=library-new`) with isolated SQLite databases (`db/data/${PROFILE}.db`), distinct base paths (`/library`, `/cloud`, `/library-new`), and PM2 process management (`ecosystem.config.js`).

---

## 2. System Architecture & Tech Stack

```
                                 +-----------------------------+
                                 | DigitalOcean Spaces (S3)    |
                                 |  - PDFs, Audio, ZIPs        |
                                 |  - High-Res Page Previews   |
                                 +--------------+--------------+
                                                ^
                                                | S3 SDK / Presigned URLs
+---------------------+           +-------------+-------------+           +----------------------+
| Client Browser      |  HTTP     | Astro Server (Node.js SSR) |  Drizzle  | SQLite Embedded DB   |
|  - HTML / Tailwind  |<--------->|  - Node adapter (port 8092)|<--------->|  - db/data/*.db      |
|  - Vue Islands      |           |  - Singlish Search Engine  |           |  - entries table     |
+---------------------+           +---------------------------+           +----------------------+
```

### 2.1 Core Technologies
- **Framework:** Astro 4.x (SSR output mode with `@astrojs/node` standalone adapter)
- **Styling:** Tailwind CSS 3.4 (with semantic themes, dark/light mode toggle, Sinhala typography support)
- **Database & ORM:** SQLite (`better-sqlite3`) with `drizzle-orm` and `drizzle-kit`
- **Dynamic Islands:** Vue 3.5 (`@astrojs/vue`) for table sorting, admin file uploader, and edit modals
- **PDF & Image Processing:** `pdfjs-dist` (legacy build) + `@napi-rs/canvas` + `sharp`
- **Search Engine:** `@pnfo/singlish-search` (strictly locked to version `1.2.5`)
- **Process Manager:** PM2 (`ecosystem.config.js`)

---

## 3. Comprehensive Log of User Concerns & Design Iterations

Throughout the refactoring process, the user meticulously reviewed the visual rendering, responsiveness, and behavioral edge cases against the original codebase. Below is the detailed record of every critical concern raised, why it arose, and how it was resolved.

### Concern 1: Icon & Name Vertical Alignment and Responsive Typography Shifts
- **User Feedback:**
  > *"vertical alignment of the icon and name seems to be off - they should both be center aligned within the row. also in the responsive view when i change the width of the screen the font sizes also seems to be changing. in xs the fontsize is ok but when i increase the width the fontsize increases a bit, then when i exit the responsive view (in chrome dev tools) the fontsize goes back to normal"*
- **Root Cause:**
  1. Flex containers used `items-start` instead of `items-center` on table row badges.
  2. Tailwind fluid typography classes (`text-sm sm:text-base md:text-lg`) caused the font to jump unexpectedly when testing across screen widths in DevTools.
- **Resolution:**
  - Aligned icons and titles using `flex items-center gap-2` with `align-middle` on `<td>` cells.
  - Standardized font sizing to a steady, readable `text-base` (16px / `1rem`) across all breakpoints, matching Vuetify's `style="font-size: 1rem;"`.

### Concern 2: UN-Alakamanda Font Removal & Sinhala Font Standards
- **User Feedback:**
  > *"remove the UN-Alakamanda-4-95.ttf font and any styles using it. instead use the un-abhaya for the topbar name that you have used UN-Alakamanda-4-95.ttf."*
- **Root Cause:**
  An obsolete stylized decorative Sinhala font (`UN-Alakamanda`) was applied to the top navigation header, which looked inconsistent with the standard Buddhist publication typography.
- **Resolution:**
  - Deleted `UN-Alakamanda-4-95.ttf` from fonts and CSS definitions.
  - Applied `font-['sinhala']` / `un-abhaya` font stack (`font-family: 'un-abhaya', sans-serif`) to the logo and header title.

### Concern 3: Missing File Type Icons & Generic File View Badges
- **User Feedback:**
  > *"please go back and check the icons used for file types in the original code. i see the excel icon (xls, xlsx), audio icon (mp3, m4a ...), photo icon (jpg, jpeg, png...) ... are missing in the new code. make sure to bring in all the icons to the table view. also when i go to the file view, i just see the extention typed in the icon box instead of the actual icon (please note that in the old code the actual icon was used in the file view as well)"*
- **Root Cause:**
  The new code simplified `getTypeInfo()` into a few hardcoded types (`pdf`, `htm`, `zip`) and defaulted everything else to a generic text badge showing the file extension.
- **Resolution:**
  - Fully mapped all file types to Material Design Icons (MDI) matching the original `stores/utils.js`:
    - **PDF:** `mdi-file-pdf-box` (`text-rose-400`)
    - **Audio (MP3 / M4A):** `mdi-volume-high` (`text-emerald-400` / `text-purple-400`)
    - **Excel (XLS / XLSX):** `mdi-microsoft-excel` (`text-emerald-500`)
    - **Word (DOC / DOCX):** `mdi-microsoft-word` (`text-sky-400`)
    - **Images (JPG / JPEG / PNG):** `mdi-image` (`text-amber-400`)
    - **HTML / Web (HTM / HTML):** `mdi-language-html5` (`text-blue-400`)
    - **Android (APK):** `mdi-android` (`text-emerald-400`)
    - **Archives (ZIP):** `mdi-folder-zip` (`text-purple-400`)
    - **Folders:** `mdi-folder` (`text-amber-500`)
  - Updated `FileView.astro` to render the genuine large MDI icon (`<i class="mdi ${typeInfo.icon} text-6xl sm:text-7xl ${typeInfo.iconColor}"></i>`) instead of raw text.

### Concern 4: Table Row Spacing, Borderless Banner Notices, & Search Focus
- **User Feedback:**
  > *"screenshot from the old code base - except for folders download count and added date are displayed for files on big screens in the tables. check the spacing between the rows and font size and set exactly to those values. currently your gap is larger and font size bit smaller.*  
  > *also when searching i am losing the focus on the search text box after the search is completed. but in the old codebase the focus is retained so i can keep typing after the results are displayed.*  
  > *on the home page and the search results page, there are banner notices at the top with boxes. remove those boxes and just keep the notice same as in the old code"*
- **Root Cause:**
  1. Astro table rows had `py-3` padding instead of Vuetify's compact density (~40px row height).
  2. Notices were wrapped in boxed `<div class="border rounded-lg bg-surface p-3">` containers instead of Vuetify's borderless `variant="text"` alert style.
  3. When submitting search queries, full page navigation reloaded the DOM and `#search-input` lost cursor focus.
- **Resolution:**
  - Tightened table row padding to `py-1.5 px-2` (`density="compact"` height).
  - Converted notices to borderless, single-line text banners with leading filled icons (`mdi-magnify` or `mdi-alert-circle text-amber-500`).
  - Added script in `Header.astro` that detects if the user is on `/search/` and executes `searchInput.focus()` with `setSelectionRange(len, len)`, keeping the cursor at the end of the text for continuous typing.

### Concern 5: Singlish Phonetic Search & Library Version Locking
- **User Feedback:**
  > *"looks right the search is now not sending the search term through the singlish converter. i am getting results only on english matches. make sure to use the exact version of the singlish library as the old codebase and convert the search query the same way we do in the old code"*  
  > *"any chance you can check the exact version of the singlish library used in the old code (check package-lock?) and see if you can use that exact same version"*
- **Root Cause:**
  The new project installed `@pnfo/singlish-search` with `"^1.2.5"`, which npm resolved to `1.2.7`, introducing subtle differences in match combinations.
- **Resolution:**
  - Inspected `/Volumes/2TB/node/file-server/package-lock.json` and verified line 3319: exact version **`1.2.5`**.
  - Pinned `package.json` to `"@pnfo/singlish-search": "1.2.5"` (removing `^`) and ran `npm install`.
  - Integrated `getPossibleMatches(query)` into `db/index.ts` so search terms like `"himi"` expand to `["හිමි", "හිමී", ...]` and query SQLite across `name`, `author`, `version`, and `description`.
  - Extended search to include folders as well as files.

### Concern 6: Search Results Table Column Structure (`බහාලුම` Parent Folder)
- **User Feedback:**
  > *"also in the old code base in seach results i see the folder as a column instead of the numdownloads. i can't point out these differences one by one, please do a through comparison"*
- **Root Cause:**
  In folder views, all items share the current folder, so showing a parent folder column is redundant (`excludeHeadings=['parent']`). In search results, files originate from many folders, so the parent folder MUST be displayed while hiding the date added column (`excludeHeadings=['dateAdded']`).
- **Resolution:**
  - Passed `excludeHeadings={['dateAdded']}` on `/search/[searchQuery].astro`.
  - Configured `EntryListTable.vue` to display the `බහාලුම` (Parent Folder) column with clickable navigation links to `${baseUrl}/${item.parentId}`.

### Concern 7: Folder-Only Home Page Column Visibility
- **User Feedback:**
  > *"in the home page which has only folders please don't show only the name and size columns (see screenshot). also check the banner notice font size."*
- **Root Cause:**
  The Home page (`/`) only contains collections/folders (ID 0). Showing empty headers for downloads and date added looked broken.
- **Resolution:**
  - Added computed property `hasFiles = computed(() => props.entries.some(e => e.type !== 'folder'))`.
  - When `hasFiles` is false, columns 2 (`බහාලුම`), 3 (`බාගත ගණන`), and 4 (`එක් කළ දිනය`) are hidden automatically, leaving ONLY:
    1. **ගොනුවේ නම** (Name)
    2. **ප්‍රමාණය** (Size)

### Concern 8: Entry Name Colors (Folders vs Files)
- **User Feedback:**
  > *"in the previous screenshots you may have noticed that the font color of file/folder names are different?"*  
  > *"geezz file names too in folder and search results views"*
- **Root Cause:**
  Initially, folder names were colored cyan while file names were colored white. In Vuetify's `EntryList.vue`, line 86 defined:
  ```css
  .entry-name { padding: 0 0.6rem 0 0.2rem; text-decoration: none; color: rgb(var(--v-theme-info)); }
  ```
  Both files AND folders rendered with the `text-info` cyan color (`#64b5f6`).
- **Resolution:**
  Applied `text-info text-base hover:text-primary transition-colors font-normal inline` to ALL entry links (files and folders) across all table views.

### Concern 9: Initial Table Sort Order & Inline File Descriptions
- **User Feedback:**
  > *"when loading the folder view for the first time, the rows must be ordered by the name. currently it is ordered by a random order until i click on the column header to sort.*  
  > *2. when the file names are the same but different desc (eg. files 2 and 805) in folder view i only see one entry in the folder view and i don't see the desc near the file name. in the old ui i see the desc near the file name in small font (please check the old code)"*
- **Root Cause:**
  1. The SQLite query in `getChildrenOf` was not sorting by name, and `EntryListTable.vue` was not enforcing sort order until user interaction.
  2. Files with identical titles (e.g. reprint editions of ancient Dhamma books) had distinguishing notes stored in `desc`/`version` (e.g. `scanned`, `1965`, `කළුසුදු`). These were omitted from the table item template.
- **Resolution:**
  - Added `.orderBy(asc(entries.name))` in `db/index.ts`.
  - Replicated lines 64-67 from original `EntryList.vue`:
    ```html
    <span v-if="item.type === 'folder'" class="text-xs text-text-muted ml-2 font-mono whitespace-nowrap opacity-80">
      ගොනු {{ item.numEntries || 0 }} කි.
    </span>
    <span v-else-if="item.version || item.desc" class="text-xs text-text-muted ml-2 font-sans opacity-80 whitespace-normal">
      {{ item.version || item.desc }}
    </span>
    ```

### Concern 10: TopNav Share Link on File View vs Folder View
- **User Feedback:**
  > *"1. the share link should only be visible in the folder view. in the screenshot it is correctly displayed in the folder view. however currently it is visible in file view too. this is not necessary since the file view already has a big share button"*
- **Root Cause:**
  `TopNav.astro` unconditionally rendered the `↗ සබැඳිය` button on all views. On `FileView.astro`, there was already a primary action button "බෙදාගැනීමට සබැඳියක් ගන්න".
- **Resolution:**
  - Added `showCopyLink: boolean = true` prop to `TopNav.astro`.
  - Passed `showCopyLink={false}` in `FileView.astro`.

### Concern 11: File View Share Button Contrast in Light Mode
- **User Feedback:**
  > *"4. in light theme the share button in file view is bit harder to read."*
- **Root Cause:**
  Light mode used a pale cyan background with low-contrast cyan text.
- **Resolution:**
  Updated styling to high-contrast classes:
  `text-sky-700 dark:text-sky-300 bg-sky-500/10 dark:bg-sky-500/20 border border-sky-500/40 hover:bg-sky-500/20 dark:hover:bg-sky-500/30`.

### Concern 12: Bottom Sort Notice Font Size & Alignment
- **User Feedback:**
  > *"3. the bottom notice indicating the sorting order must be the same font size and alignment as the top banner notices we display."*
- **Resolution:**
  Updated the sort status indicator at the bottom of the table to `flex items-center gap-2.5 text-text text-base py-2 border-t border-border/30 mt-2 opacity-95` with `<i class="mdi mdi-sort text-info text-xl"></i>`.

---

## 4. Deep Technical Post-Mortems: The Toughest Bugs

### 4.1 The Great "GB in Red and MB in Green" & Vue SSR Hydration Bug

#### The Symptom
The user requested:
> *"render the text GB in red and MB in green indicating the GB size is bigger"*  
> *"both of the above are still broken. see the screenshot with red circles above. you don't have to do maths, just render the text GB in red and MB in green. it is very easy"*  
> *"no the error must be something else. i still see the Hydration completed but contains mismatches error in the console and still see the MB in RED and GB in green. check the screenshot as well"*

In screenshots and browser inspection, rows with `4.8 MB` were rendering `<span class="size-unit GB">MB</span>` in red, while rows with `3.6 GB` were rendering in green.

#### The Root Cause Investigation
1. **Initial Red Herring (The Math Fallacy):**
   We initially suspected byte conversion math (`Math.log(bytes) / Math.log(1024)`). We replaced it with explicit threshold constants (`bytes >= 1073741824`). Yet the bug persisted in the browser!
2. **The Server Inspection Reality:**
   Running `curl http://localhost:8092/library-new/` showed that the server-rendered HTML was completely correct!
   `<span class="size-unit MB">MB</span>` was rendered for 4.8 MB, and `<span class="size-unit GB">GB</span>` for 3.6 GB.
3. **The True Culprit: SSR Hydration Mismatch:**
   - In `index.astro`, SQLite retrieved items ordered by `asc(entries.name)`.
   - In `EntryListTable.vue`, a computed property `sortedEntries` ran on component mount and sorted the items using `localeCompare(b, 'si')`.
   - Node.js server environment and browser client environment had slight differences in ICU collation for complex Sinhala Unicode ligatures!
   - Because the client sort produced an array in a slightly different order than the server HTML, Vue 3's hydration engine tried to patch existing DOM elements in place without recreating them.
   - During DOM element patching, existing text nodes and class attributes got tangled across adjacent rows, resulting in `GB` CSS classes staying stuck on `MB` elements!
   - This also explained why the user saw `Hydration completed but contains mismatches` in the console.

#### The Permanent Solution: The SSR Hydration Guard
In `astro-server/src/components/EntryListTable.vue`:
```javascript
/**
 * CRITICAL SSR HYDRATION GUARD:
 * `isUserSorted` tracks whether the user has explicitly clicked a table header column to re-sort.
 * On initial SSR rendering and client-side hydration, `isUserSorted` remains `false`.
 * This guarantees that `sortedEntries` returns `processedItems` in the EXACT array order
 * sent by the server database query (which is already pre-sorted by name ascending in SQLite).
 * Returning the exact SSR array order on initial mount prevents "Hydration completed but contains mismatches"
 * errors and prevents element attribute/class corruption in Vue 3 + Astro.
 */
const isUserSorted = ref(false);

const sortedEntries = computed(() => {
  if (!isUserSorted.value) {
    return processedItems.value; // Exact match with SSR HTML!
  }
  // Client-side sorting only executes when the user interacts
  const list = [...processedItems.value];
  list.sort(...);
  return list;
});
```
And for the color classes, straightforward Tailwind class bindings:
```html
<span
  :class="(item.sizeInfo.unit === 'GB' || item.sizeInfo.unit === 'TB') ? 'text-rose-500 dark:text-rose-400 font-medium' : 'text-emerald-500 dark:text-emerald-400 font-medium'"
  class="text-xs ml-1 font-sans"
>
  {{ item.sizeInfo.unit }}
</span>
```
Once `isUserSorted` was guarded, the hydration error vanished completely, and all colors stayed 100% accurate.

---

### 4.2 The PDF Preview CanvasGraphics Polyfill Crash

#### The Symptom
When generating PDF preview images on the server via `api/admin/generate-previews.ts`, the Node process crashed with:
```
Error rendering PDF page 1: Error: Value is none of these types `String`, `Path`, 
    at CanvasGraphics.paintChar (pdfjs-dist/legacy/build/pdf.mjs:13432:15)
    at CanvasGraphics.showText (pdfjs-dist/legacy/build/pdf.mjs:13595:16)
    code: 'InvalidArg'
```

#### The Root Cause
1. `@napi-rs/canvas` is a high-performance native Node.js canvas library written in Rust.
2. Many scanned Sinhala Buddhist books contain embedded Type3 or custom subset TrueType fonts.
3. When `pdfjs-dist` executes `paintChar` for certain embedded font glyphs, it passes undefined or empty SVG path arguments into the canvas `Path2D` constructor.
4. The Rust binding for `Path2D` strictly validates its input arguments: if the argument is neither a valid SVG string nor an existing `Path2D` pointer, it throws `InvalidArg: Value is none of these types String, Path`.

#### The Permanent Solution
In `astro-server/src/pages/api/admin/generate-previews.ts`:
1. **Safe `Path2D` Polyfill:** Subclassed `Path2D` to intercept non-string/non-path arguments and substitute an empty path `""`:
   ```typescript
   class SafePath2D extends NapiPath2D {
     constructor(path?: any) {
       if (typeof path === 'string') {
         super(path);
       } else if (path instanceof NapiPath2D) {
         super(path);
       } else {
         super('');
       }
     }
   }
   (globalThis as any).Path2D = SafePath2D;
   ```
2. **`disableFontFace: true`:** Configured `pdfjsLib.getDocument({ data, disableFontFace: true })` so that font glyphs are parsed as standard vector path outlines rather than triggering OS font-face loader errors.
3. **Sharp Compression Pipeline:** Rendered at scale 2.5 on canvas and piped to `sharp` to resize to a crisp `1500px` height at `quality: 85`.

---

## 5. Metadata Schema & Data Modeling Architecture

The database schema in `astro-server/db/schema.ts` was expanded to support rich bibliographical metadata:

```typescript
export const entries = sqliteTable('entries', {
  id: integer('id').primaryKey(),
  name: text('name').notNull(),                        // Clean title without tags
  fileName: text('file_name'),                         // Original S3 filename
  version: text('version'),                            // Version / edition tag (e.g. "1.0", "v2", "කළුසුදු")
  description: text('description'),                    // Full rich text HTML overview / summary
  author: text('author'),                              // Author or speaker name
  publishingYear: integer('publishing_year'),          // Year of original publication (e.g. 1965)
  printedYear: integer('printed_year'),                // Year of physical printing (e.g. 2018)
  type: text('type').notNull(),                        // 'folder', 'pdf', 'mp3', 'zip', 'htm', etc.
  sizeBytes: integer('size_bytes').notNull().default(0),
  numPages: integer('num_pages'),                      // Auto-detected total page count for PDFs
  numEntries: integer('num_entries').notNull().default(0), // Folder file count
  s3Key: text('s3_key').notNull(),                     // Relative S3 key
  parentId: integer('parent_id').notNull().default(0), // Hierarchy link (0 = root)
  downloads: integer('downloads').notNull().default(0),
  previewPages: text('preview_pages'),                 // JSON array of 1-based page numbers: "[1, 2]"
  isPublished: integer('is_published').notNull().default(1),
  dateAdded: text('date_added').notNull(),             // YYYY-MM-DD
  updatedAt: text('updated_at').notNull()
});
```

### 5.1 Filename Regex Parsing Convention
The file naming convention used by the storage bucket is:
```
name[version]{entryId}.type
```
Example: `නාමමාලා - වස්කඩුවේ සුභූති හිමි[1965]{200}.pdf`
- **Name:** `නාමමාලා - වස්කඩුවේ සුභූති හිමි`
- **Version:** `1965`
- **Entry ID:** `200`
- **Type:** `pdf`

Parsed in `astro-server/src/utils/s3.ts`:
```typescript
const res = /^(.+?)(?:\[(.*)\])?(?:\{(\d+)\})?(?:\.(\w+))?$/.exec(fileName);
```

---

## 6. Admin Interface & Management Workflows

### 6.1 Admin Architecture (`astro-server/src/components/admin/`)
The Admin interface is protected by a unified password challenge and consists of:
- **`AdminDashboard.vue`:** Primary view listing all entries with search, folder navigation, status toggles (Publish / Unpublish), and action buttons.
- **`UploadModal.vue`:** Upload modal supporting drag-and-drop file uploads, folder destination selector, and 5 metadata fields (Version, Author, Publishing Year, Printed Year, and a Rich Text HTML description editor with bold, italic, paragraph, and list toolbars).
- **`EditModal.vue`:** In-place metadata editor for existing entries, with page preview gallery and direct preview regeneration.
- **`PdfPreviewPickerModal.vue`:** Interactive modal allowing admins to enter comma-separated PDF page numbers (e.g. `1, 4, 12`), generating high-res JPEG previews stored in S3 at `previews/{id}-{index}.jpg`.

### 6.2 Publishing vs Deleting
- **Unpublish (`isPublished = 0`):** Soft hides the entry from public view and search immediately. Preserves all files and metadata in SQLite and S3.
- **Delete:** Permanently removes the SQLite database record and deletes associated S3 assets (`s3Key` and `previews/{id}-*.jpg`).

---

## 7. Search Engine Implementation (`Singlish`)

The search pipeline in `astro-server/db/index.ts` combines phonetic transliteration with indexed SQL queries:
1. User types in Singlish or Sinhala Unicode (e.g. `himi`, `dhamma`, `abhidhamma`).
2. `@pnfo/singlish-search` (`getPossibleMatches`) generates phonetic Sinhala permutations (`["හිමි", "හිමී"]`).
3. Queries SQLite using `like(entries.name, '%term%')`, `like(entries.author, '%term%')`, `like(entries.version, '%term%')`, and `like(entries.description, '%term%')`.
4. Returns both matching folders and files with resolved `parentName` strings for breadcrumb rendering.
5. On the search results page (`/search/[searchQuery]`), `#search-input` automatically regains focus with the cursor placed at the end of the text.

---

## 8. Deployment & Multi-Profile Orchestration

The application is deployed with PM2 via `astro-server/ecosystem.config.js`:
```javascript
module.exports = {
  apps: [
    {
      name: "library-new-test",
      script: "dist/server/entry.mjs",
      env: {
        PORT: 8092,
        PROFILE: "library",
        BASE_URL: "/library-new",
      },
    },
    {
      name: "library-prod",
      script: "dist/server/entry.mjs",
      env: {
        PORT: 8090,
        PROFILE: "library",
        BASE_URL: "/library",
      },
    },
    {
      name: "cloud-prod",
      script: "dist/server/entry.mjs",
      env: {
        PORT: 8091,
        PROFILE: "cloud",
        BASE_URL: "/cloud",
      },
    }
  ]
};
```

---

## 9. Crucial Rules for Future AI Coding Models & Engineers

If you are an AI assistant or human developer modifying this codebase in the future, **YOU MUST ADHERE TO THE FOLLOWING RULES**:

1. **NEVER Trigger Initial Client Sorting in `EntryListTable.vue`:**
   Always keep `isUserSorted = ref(false)` initially. Never run client-side `.sort()` before user click. Doing so will reintroduce the `Hydration completed but contains mismatches` error and scramble table row colors.
2. **DO NOT Overcomplicate Size Unit Colors:**
   Size unit colors are simple:
   - If unit is `'GB'` or `'TB'`: **Red** (`text-rose-500 font-medium`).
   - If unit is `'MB'`, `'KB'`, or `'B'`: **Green** (`text-emerald-500 font-medium`).
   Do not add float threshold comparisons or logarithmic math to decide colors in the UI template.
3. **DO NOT Re-introduce Box Containers around Banner Notices:**
   All notices at the top of pages and bottom of tables must remain borderless, clean text rows with leading MDI icons. Do not put `<div class="border rounded-lg p-4">` around them.
4. **DO NOT Show Empty Columns on Folder-Only Pages:**
   If a folder contains only other folders (`hasFiles === false`, like the Home page), show ONLY the Name and Size columns. Hide Downloads, Date Added, and Parent columns.
5. **Always Retain Search Input Focus:**
   Whenever search pages or search inputs are re-rendered, ensure the `#search-input` focus and selection range preservation logic in `Header.astro` remains intact.
6. **Preserve the `SafePath2D` Polyfill in `generate-previews.ts`:**
   Do not remove `SafePath2D` or set `disableFontFace: false`. Doing so will cause `@napi-rs/canvas` to throw `InvalidArg: Value is none of these types String, Path` when rendering custom Sinhala fonts from PDFs.
7. **Lock `@pnfo/singlish-search` to Exact Version `1.2.5`:**
   Do not use caret `^1.2.5` in `package.json`. Keep it locked to exact `"1.2.5"` to prevent breaking transliteration matches.
8. **Preserve Isolated Workspaces:**
   Keep all refactored Astro code in `/Volumes/2TB/node/file-server/astro-server/`. Do not modify the legacy Nuxt 3 root directory unless specifically instructed.

---

## 10. File Serving, Mobile Browsers & Download Architecture

Serving large static assets (PDF books, MP3 dhamma talks, zipped archives, HTML bundles up to 300MB) from S3-compatible cloud storage (DigitalOcean Spaces) across mobile browsers, desktop browsers, and in-app webviews presents critical edge-case pitfalls.

This section documents the challenges discovered in production, the failed approaches and why they failed, the holistic solution implemented, and design considerations for future iterations.

### 10.1 The Operational Context & Hardware Constraints

1. **DigitalOcean Spaces Storage:**
   - Files are stored in the Singapore (`sgp1`) Spaces bucket `tipitaka`.
   - Accessible via S3 API (`GetObjectCommand`) or presigned S3 URLs (`@aws-sdk/s3-request-presigner`).
   - S3 AWS Signature Version 4 (SigV4) signs query parameters (`X-Amz-Signature`, `X-Amz-Expires`, `X-Amz-SignedHeaders`, etc.).
2. **Production Node.js Droplet Constraints:**
   - Strictly memory-constrained environment running with Node old space limit:
     `NODE_OPTIONS: "--max_old_space_size=280"` (~280MB RAM).
   - `server/index-handler.js` includes an active safety check: if memory usage exceeds 300MB (`process.memoryUsage().rss / 1024 / 1024 > 300`), the process kills itself (`process.exit(1)`) to avoid freezing the system.
   - Nginx reverse-proxy in front of the Node service on port 8090 (`/library`) has:
     ```nginx
     location /library {
         proxy_max_temp_file_size 0;
         proxy_pass http://localhost:8090;
     }
     ```
3. **Diverse File Types & Sizes:**
   - PDFs (scanned books, manuscripts from 1MB up to 250MB).
   - Audio files (`.mp3`, `.m4a` Dhamma sermons needing byte-range seeking for `<audio controls>`).
   - Software and bulk archives (`.zip`, `.apk` up to 300MB).
   - Web documents (`.html`, `.htm`, `.txt`).

---

### 10.2 Discovered Problems & Edge Cases

#### Problem A: Mobile Chrome / Mobile Safari Blank Screen on 302 Redirect to PDF
- **Symptom:**
  When a user tapped the download button on a mobile phone (e.g. `https://tipitaka.lk/library/1220/download`), the server issued an HTTP 302 redirect pointing directly to the S3 signed URL.
  Mobile Chrome displayed a completely blank white screen. Tapping the address bar showed a status bar saying *"Loading PDF"* with the S3 URL underneath, and the PDF only rendered if the user manually tapped that URL bar notification. Mobile Safari had similar silent rendering stalls. Desktop browsers worked normally.
- **Root Cause:**
  Mobile Chrome and Safari handle cross-origin HTTP 302 redirects to PDF MIME types differently from desktop browsers. Rather than delegating the cross-origin 302 stream cleanly into their built-in PDF viewer sandboxes, the mobile browser's embedded PDF viewer stalls waiting for direct top-level navigation context.

#### Problem B: Facebook In-App Browser & S3 SigV4 `SignatureDoesNotMatch` Error
- **Symptom:**
  When links to files (`https://tipitaka.lk/library/1220`) were shared on Facebook mobile, clicking the link opened the page inside Facebook's In-App Browser (IAB / webview). When the user clicked "බාගත කරන්න" (Download), Facebook IAB showed an OS prompt asking permission to open an external app (Chrome/Safari). After opening Chrome, Chrome showed an XML error page instead of the file:
  ```xml
  <Error>
    <Code>SignatureDoesNotMatch</Code>
    <Message>The request signature we calculated does not match the signature you provided. Check your key and signing method.</Message>
    <Resource>tipitaka/library/...pdf</Resource>
  </Error>
  ```
- **Root Cause:**
  When the endpoint returned an HTTP 302 redirect directly to the S3 URL with `ResponseContentDisposition: attachment`, Facebook's in-app webview intercepted the file download, launched the system default browser (Chrome), and automatically appended Facebook tracking parameters to the URL:
  `&fbclid=IwSU9FWAUU8NlwZG...`
  Under S3 SigV4, the cryptographic HMAC signature is computed over the exact query string parameters defined when signing (`X-Amz-SignedHeaders`). Appending `fbclid` changes the query string received by S3 without updating `X-Amz-Signature`, causing S3 to immediately reject the request with `SignatureDoesNotMatch`.

#### Problem C: Node Proxy / Streaming Proxy Deadlock (`504 Gateway Time-out`)
- **Symptom:**
  Attempting to solve Problems A and B by proxying the S3 stream directly through the Node server (`sendStream(event, res.Body)` via AWS SDK `GetObjectCommand`) initially worked on small tests, but rapidly degraded into `504 Gateway Time-out` from Nginx across all downloads and audio playback.
- **Root Causes:**
  1. **Nginx Buffer Deadlock:** Nginx was configured with `proxy_max_temp_file_size 0;` (disabling buffering to disk) alongside default `proxy_buffering on;`. When streaming large responses through Node to mobile connections, buffer backpressure stalled chunk transmission, causing socket timeouts between Nginx and Node.
  2. **Socket Pool Exhaustion:** The AWS SDK S3 client uses an internal HTTP/HTTPS agent with a default connection pool limit (50 sockets). If a mobile client disconnected, paused audio playback, or had network fluctuations, Node streams remained open. Because AWS SDK's `IncomingMessage` did not have an explicit socket abort bound to early client disconnects (`res.on('close')`), all 50 connection sockets leaked into lingering states within hours, completely starving all subsequent requests to S3 and triggering 504 Gateway Timeouts.
  3. **Memory Limits & Droplet OOM Risk:** Streaming 100MB–300MB files through a Node process with a hard 280MB heap limit (`--max_old_space_size=280`) risks immediate process crashes if multiple users download simultaneously or if internal stream chunks buffer in memory.

---

### 10.3 Solution Implemented: The Dual-Route Architecture & HTML Bridge

To satisfy all constraints without overloading the server or breaking browser security/signing mechanics, a **Dual-Route Architecture** was implemented in `server/routes/[fileId]/download.js`:

```
                           +---------------------------+
                           | User Requests /download   |
                           +-------------+-------------+
                                         |
                       +-----------------+-----------------+
                       |                                   |
                Is Audio (MP3/M4A)                 Document / Download
               or Direct (?inline=1)               (PDF, HTML, ZIP, APK)
                       |                                   |
                       v                                   v
             +--------------------+               +------------------+
             | Direct HTTP 302 to |               | Return Fast HTML |
             | S3 Presigned URL   |               | Bridge Page      |
             +--------------------+               +--------+---------+
                       |                                   |
                       v                                   v
          - <audio controls> plays byte-ranges   - Loads in FB webview without error
          - Zero Node RAM / bandwidth            - Auto-redirects via JS replace()
          - DO Spaces CDN handles caching        - Safe fallback <meta refresh> & link
                                                 - Bypasses mobile Chrome 302 bug
                                                 - No ?fbclid appended to S3 URL
```

#### 1. Audio & Direct Stream Pathway (`mp3`, `m4a`, `?inline=1`, `?direct=1`)
Audio players require byte-range requests (`Range: bytes=0-` / HTTP 206) for seeking. Proxying streaming audio through Node risks socket exhaustion and buffer lag.
- The server generates an S3 presigned URL with 1-hour expiry.
- Directly issues `sendRedirect(event, signedUrl, 302)`.
- The browser `<audio controls>` element follows the redirect directly to DigitalOcean Spaces CDN, supporting smooth seeking with 0 Droplet RAM and 0 Droplet bandwidth.

#### 2. HTML Bridge Pathway (PDF, HTML, ZIP, APK, etc.)
Instead of issuing a direct 302 redirect or piping megabytes through Node memory:
- The server increments the download counter in SQLite / memory.
- Generates an S3 presigned URL for the target object (`GetObjectCommand`).
- Instantly returns a lightweight (< 1KB), responsive HTML page with:
  1. **Instant Client-Side Navigation:** `window.location.replace(signedUrl)` executes immediately on document parse.
  2. **Zero-Second Meta Refresh:** `<meta http-equiv="refresh" content="0; url=...">` guarantees navigation if JavaScript is restricted.
  3. **Direct User Action Button:** A styled fallback button `ගොනුව විවෘත කරන්න / Open File` allows the user to open the file manually if an in-app browser pauses auto-redirection.

#### Why This Resolves Every Problem:
1. **Solves the Facebook Webview `SignatureDoesNotMatch`:** Facebook IAB loads the HTML bridge page natively as a standard web page without prompting. The subsequent client-side navigation loads the S3 URL directly without Facebook appending `&fbclid=` to the S3 query parameters.
2. **Solves the Mobile Chrome / Safari Blank PDF Screen:** Because the navigation is executed from within an active, loaded document context (`window.location.replace`), mobile Chrome treats it as top-level user navigation rather than an opaque cross-origin 302 redirect, correctly rendering the PDF in the native mobile viewer.
3. **Solves the Droplet 504 Gateway Timeout & OOM Crashes:** The Node.js server finishes handling the request in under 5 milliseconds by returning a 1KB string. Large files (50MB, 100MB, 300MB) stream directly from DigitalOcean Spaces CDN to the client device, keeping Droplet memory well under the 280MB limit.

---

### 10.4 Guidelines for Future Work & Astro Server Migration

When porting or evolving the file download architecture into `astro-server/` (`src/pages/[fileId]/download.ts`):

1. **Do NOT Stream Large S3 Files Through Node:**
   Never pipe S3 `GetObjectCommand` streams through Astro/Node routes unless files are guaranteed to be under 5MB or the deployment infrastructure has substantial dedicated bandwidth and memory.
2. **Preserve the HTML Bridge for Document Downloads:**
   Maintain the client-side redirect bridge for PDFs, ZIPs, and executables to avoid mobile Chrome/Safari 302 stalls and Facebook In-App Browser signature stripping.
3. **Preserve Direct 302 for Audio Files:**
   Audio elements (`<audio>`) and podcast players cannot parse HTML bridge pages. Audio files MUST receive direct HTTP 302 redirects to S3 signed URLs so they can perform byte-range requests.
4. **Never Force `ResponseContentDisposition: attachment` on S3 Signed URLs for In-App Browsers:**
   Setting `ResponseContentDisposition: attachment` in presigned S3 URLs causes Facebook and Instagram webviews to forcibly spawn external browser intents, which leads to `fbclid` query injection and signature mismatch errors. Let the browser decide inline presentation or let the user save from the native PDF toolbar.

---
*End of Design Concerns & Architecture Document.*
