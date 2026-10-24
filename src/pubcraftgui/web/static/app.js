/* PubCraft — logica client.
 * Dipendenze: marked (markdown), pdf.js (anteprime PDF). Tutto incorporato. */
"use strict";

try {
  if (localStorage.getItem("pubcraft.theme") === "dark") {
    document.documentElement.classList.add("dark-mode");
    if (document.body) document.body.classList.add("dark-mode");
  }
} catch {}

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const isMac = /Mac|iPhone|iPad/.test(navigator.platform);

const ta = $("ta"), prevFrame = $("prevFrame");
const chapterList = $("chapterList");

let state = { vault: "", book: null };   // vault + libro aperto
let defaultParentDir = "";
let autoProjectFolder = true;
let currentFile = "";                    // capitolo aperto
let currentFileVault = "";               // vault del capitolo correntemente aperto
let isProjectSwitching = false;          // blocco attivo durante transizione tra progetti
let isFlushingForSwitch = false;         // flag per permettere flush finale prima del cambio vault
let projectTransitionId = 0;             // id progressivo delle transizioni di progetto
let isBuilding = false;                  // blocco durante le operazioni di build
let currentTitle = "";                   // suo titolo (per l'anteprima)
let dirty = false, saveTimer = null;
let saveRevision = 0, lastSavedRevision = 0;
let saveQueue = Promise.resolve();
let savePromise = null;
let chapterNavId = 0;
let isLoadingChapter = false;
let fileVersion = "";
let styleCssText = "";                   // override CSS dello stile del libro
let bodyFontCssText = "";
let sceneBreakCssText = "";
let customPrintDir = "";
let printConfigOutDir = "";
let styleSampleRequestId = 0;

let recentsData = [];
let workspaceDir = "";
let activeRecentsTab = "all";
let selectedRecentPath = null;
let activeTutorialTab = "guide";

marked.use({ gfm: true, breaks: true });
if (window.pdfjsLib) {
  pdfjsLib.GlobalWorkerOptions.workerSrc = "/static/pdf.worker.min.js";
}

function formatMarkdownForBook(raw) {
  if (!raw) return "";
  const lines = raw.split("\n");
  const out = [];
  let inCode = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();

    if (trimmed.startsWith("```")) {
      inCode = !inCode;
      out.push(line);
      continue;
    }
    if (inCode) {
      out.push(line);
      continue;
    }

    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith(">") || trimmed.startsWith("|") ||
        trimmed.startsWith("- ") || trimmed.startsWith("* ") || trimmed.startsWith("+ ") ||
        /^\d+\.\s/.test(trimmed) || trimmed.startsWith("![") ||
        trimmed === "***" || trimmed === "* * *" || trimmed === "---") {
      out.push(line);
      continue;
    }

    out.push(line);
    if (i + 1 < lines.length) {
      const nextTrimmed = lines[i + 1].trim();
      if (nextTrimmed && !nextTrimmed.startsWith("#") && !nextTrimmed.startsWith(">") &&
          !nextTrimmed.startsWith("|") &&
          !nextTrimmed.startsWith("- ") && !nextTrimmed.startsWith("* ") && !nextTrimmed.startsWith("+ ") &&
          !/^\d+\.\s/.test(nextTrimmed) && !nextTrimmed.startsWith("![") && !nextTrimmed.startsWith("```") &&
          nextTrimmed !== "***" && nextTrimmed !== "* * *" && nextTrimmed !== "---") {
        out.push("");
      }
    }
  }
  return out.join("\n");
}

const projectMutationPaths = new Set([
  "/api/newchapter", "/api/deletechapter", "/api/reorder", "/api/setstyle",
  "/api/setfont", "/api/setopening", "/api/setscenebreak", "/api/addcover",
  "/api/delcover", "/api/useasepubcover", "/api/uploadimage",
  "/api/settitle", "/api/note-to-chapter",
]);
let activeProjectMutations = 0;

function updateProjectLock() {
  const busy = isProjectSwitching || isBuilding;
  ["topbar", "app", "newChapterModal", "newProjectModal", "printModal", "tableModal"].forEach(id => {
    if ($(id)) $(id).inert = busy;
  });
}

let isDelegatingApi = false;
async function api(path, opts = {}) {
  if (!isDelegatingApi && typeof window !== "undefined" && window.api && window.api !== api) {
    isDelegatingApi = true;
    try {
      return await window.api(path, opts);
    } finally {
      isDelegatingApi = false;
    }
  }
  const method = (opts.method || "GET").toUpperCase();
  const cleanPath = path.split("?")[0];
  const isNoteMutation = (cleanPath === "/api/delnote" || cleanPath === "/api/note") && (method === "POST" || method === "DELETE");
  const mutation = projectMutationPaths.has(cleanPath) || isNoteMutation;
  if (mutation && !isFlushingForSwitch && (isProjectSwitching || isBuilding)) {
    throw new Error(t("loading_chapter"));
  }
  const originVault = (state && state.vault) || "";
  if (mutation) activeProjectMutations++;
  try {
    const res = await fetch(path, {
      ...opts,
      headers: { "Content-Type": "application/json", ...opts.headers,
        ...(mutation ? { "X-PubCraft-Vault": originVault } : {}),
      },
    });
    let body = {};
    try { body = await res.json(); } catch { /* vuoto */ }
    if (!res.ok) throw new Error(body.error || res.statusText);
    if (mutation && typeof markPublicationPreviewStale === "function") {
      markPublicationPreviewStale();
    }
    return body;
  } finally {
    if (mutation) activeProjectMutations--;
  }
}

const tabClientId = "tab_" + Math.random().toString(36).slice(2, 10) + "_" + Date.now().toString(36);
let lastClientDirtySent = null;
let clientDirtyInFlight = null;
let clientHeartbeatTimer = null;

async function notifyClientDirty(isHeartbeat = false) {
  const isDirty = !!(dirty || (typeof noteDirty !== "undefined" && noteDirty) || (typeof saveRevision !== "undefined" && typeof lastSavedRevision !== "undefined" && saveRevision > lastSavedRevision) || (typeof noteRevision !== "undefined" && typeof lastSavedNoteRevision !== "undefined" && noteRevision > lastSavedNoteRevision));
  const curVault = (state && state.vault) || "";
  const key = `${isDirty}:${curVault}`;

  if (isDirty) {
    if (!clientHeartbeatTimer) {
      clientHeartbeatTimer = setInterval(() => {
        notifyClientDirty(true);
      }, 2500);
    }
  } else {
    if (clientHeartbeatTimer) {
      clearInterval(clientHeartbeatTimer);
      clientHeartbeatTimer = null;
    }
  }

  if (!isHeartbeat && lastClientDirtySent === key && !clientDirtyInFlight) return;
  lastClientDirtySent = key;

  const p = fetch("/api/client-dirty", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      client_id: tabClientId,
      dirty: isDirty,
      vault: curVault,
      description: isDirty ? ((typeof noteDirty !== "undefined" && noteDirty) ? "note" : "chapter") : ""
    })
  }).catch(() => {}).finally(() => {
    if (clientDirtyInFlight === p) clientDirtyInFlight = null;
  });
  clientDirtyInFlight = p;
  await p;
}

const I18N = {
  "en": {
    "chapter": "Chapter",
    "cancel": "Cancel",
    "loading": "Loading…",

    "focus_exit_title": "Exit Focus Mode (Esc)",
    "focus_toggle_font_title": "Toggle font: Typewriter / Serif",
    "focus_toggle_super_focus_title": "Toggle Super Focus (vertically centered text and continuous typewriter scrolling like iA Writer)",
    "focus_select_chapter_title": "Select chapter",

    "btn_new_project": "✨ New Book…",
    "btn_new_project_title": "Create a new book project (folder and book.yaml)",
    "btn_open": "Open…",
    "btn_open_title": "Open an existing book folder or import a file",
    "btn_new_chapter": "＋ New Chapter",
    "btn_new_chapter_title": "New chapter in open book",
    "btn_style": "Style: ",
    "btn_style_title": "Book style: applies to print, PDF, and preview",
    "btn_opening": "Opening: ",
    "btn_opening_title": "Chapter opening: drop cap, small caps, or plain",
    "btn_watch": "👁️ Live Watch",
    "btn_watch_title": "Open synchronized real-time print preview",
    "btn_kindle": "Kindle EPUB",
    "btn_kindle_title": "Generate EPUB for Kindle (KDP)",
    "btn_print": "Print / PDF…",
    "btn_print_title": "Generate print.html and PDF for printing",
    "save_ready": "Ready",
    "save_saving": "Saving…",
    "save_saved": "Saved ",
    "save_error": "Save error",
    "conflict_detected": "Conflict detected",
    "save_failed": "Save failed",
    "save_failed_msg": "Unable to save current chapter before continuing.\nYour entered text is preserved.",
    "save_failed_change_ch": "Unable to save current chapter. Your entered text has been preserved.\nPlease resolve the error or retry before changing chapter.",
    "btn_retry_save": "Retry Saving",
    "btn_retry": "Retry",
    "covers_header": "Covers",
    "btn_add_cover": "＋ Add…",
    "untitled": "Untitled",
    "empty_title": "Start a new book",
    "empty_desc": "You can create a new project from scratch or open an existing folder (Markdown vault).",
    "btn_create_new_empty": "✨ Create new book…",
    "btn_open_folder_empty": "📁 Open existing folder…",
    "words_count": "{count} words",
    "words_short": "words",
    "vw_editor": "Editor",
    "vw_split": "Editor + Preview",
    "vw_preview": "Preview",
    "editor_placeholder": "Write here… auto-saving is enabled.",
    "open_modal_title": "Open or create book",
    "open_choice_new": "✨ Create a new book from scratch",
    "open_choice_folder": "📁 Existing book folder (Markdown vault)",
    "open_choice_archive": "📕 Import project archive → convert to Markdown",
    "open_choice_docx": "📄 Import manuscript (.docx, .vellum, .md) → convert to book",
    "btn_cancel": "Cancel",
    "np_modal_title": "✨ Create New Book Project",
    "np_desc": "A dedicated folder (Markdown vault) will be created ready for writing, with book.yaml, initial chapter, and folders for covers and images.",
    "np_book_title_label": "Book title *",
    "np_book_title_ph": "e.g. The River's Secret",
    "np_author_label": "Author name",
    "np_author_ph": "e.g. John Doe",
    "np_parent_dir_label": "Parent folder (where to save project)",
    "np_parent_dir_ph": "Select folder...",
    "btn_browse": "Browse…",
    "np_folder_name_label": "Folder name",
    "np_folder_name_ph": "e.g. the-rivers-secret",
    "np_initial_style_label": "Initial editorial style",
    "btn_create_project": "Create Project",
    "fs_modal_title": "Choose…",
    "fs_home": "🏠 Home",
    "fs_volumes": "📂 Volumes",
    "fs_up": "↑ Up",
    "fs_new_folder": "New folder…",
    "fs_select": "Choose",
    "fs_select_file": "Choose File",
    "fs_select_folder": "Choose Folder",
    "fs_select_files_count": "Choose {n} files",
    "fs_open_folder": "📁 Open this folder",
    "fs_new_folder_prompt": "New folder name:",
    "btn_report_issue": "Report Issue",
    "btn_report_issue_title": "Report an issue or diagnostic problem",
    "report_modal_title": "🛠️ Report an Issue",
    "report_privacy_notice": "A local diagnostic report is prepared with version, environment, and recorded errors. No manuscript text, chapter content, PDF, or personal data is attached or sent automatically.",
    "report_desc_label": "Describe what you were doing or what is not working *",
    "report_desc_ph": "e.g. Tried to export print PDF but the operation failed...",
    "report_expected_label": "Expected result (optional)",
    "report_observed_label": "Observed result (optional)",
    "report_recent_errors_label": "Recent system errors recorded:",
    "report_no_recent_errors": "No recent system errors recorded.",
    "report_preview_title": "Generated Report Preview:",
    "report_btn_prepare": "Prepare Report",
    "report_btn_copy": "📋 Copy Report",
    "report_btn_export": "💾 Export to file…",
    "report_copied_msg": "Copied to clipboard!",
    "empty_folder": "Empty folder",
    "print_modal_title": "PDF / Print — Settings",
    "print_modal_desc": "Book style (saved in vault, applies to print, PDF, and preview):",
    "style_label": "Style",
    "body_font_label": "Body font",
    "scene_break_label": "Scene breaks",
    "opening_label": "Chapter opening",
    "opening_dropcap": "Drop cap",
    "opening_smallcaps": "Small caps (Adelphi/Penguin)",
    "opening_none": "Plain flush",
    "trim_label": "Trim size",
    "pages_label": "Estimated pages",
    "pages_est_ph": "≈ {pages} ({words} words)",
    "bleed_label": "full bleed",
    "preview_chapter_label": "Preview chapter:",
    "open_full_viewprint": "📖 View full formatted book",
    "dest_folder_lbl_html": "Destination: <b>out-stampa/</b> (inside book folder)",
    "dest_folder_custom": "Destination: {dir}",
    "btn_pick_other_dir": "Choose another folder…",
    "kdp_proof_note": "The final proof remains the KDP Print Previewer.",
    "btn_start_build": "Start build",
    "preview_no_text": "No text in this chapter yet.",
    "preview_empty": "This chapter has no text yet.",
    "no_chapters_yet": "No chapters",
    "new_chapter_title": "Add to book",
    "nc_type_label": "Section type",
    "nc_type_chapter": "📖 Standard chapter",
    "nc_type_dedication": "📜 Dedication (Front matter)",
    "nc_type_epigraph": "✒️ Epigraph (Front matter)",
    "nc_type_preface": "📑 Preface / Introduction (Front matter)",
    "nc_type_acknowledgments": "🙏 Acknowledgments (Back matter)",
    "nc_type_about": "👤 About the Author (Back matter)",
    "nc_title_label": "Title",
    "nc_title_ph": "e.g. Chapter 1",
    "nc_position_label": "Position",
    "nc_pos_after": "Immediately after selected chapter",
    "nc_pos_end": "At the end of the book",
    "nc_pos_start": "At the beginning of the book",
    "btn_add": "Add",
    "default_dedication": "Dedication",
    "default_epigraph": "Epigraph",
    "default_about_author": "About the Author",
    "default_acknowledgments": "Acknowledgments",
    "default_preface": "Preface",
    "modal_close": "Close",
    "modal_error": "Error",
    "build_running_title": "Building in progress…",
    "build_running_desc": "Please wait, compiling with pubcraft…",
    "build_pdf_success": "PDF Created Successfully ✔",
    "build_success": "Done ✔",
    "btn_open_pdf": "Open PDF 📄",
    "btn_print_browser": "Print / Save as PDF now (Browser) 🖨",
    "btn_show_in_finder": "Show in Finder",
    "btn_show_in_folder": "Show in folder",
    "unsaved_changes_title": "Unsaved changes",
    "no_book_found_title": "No book found",
    "no_book_found_desc": "The selected folder does not contain Markdown files (.md) or a book.yaml file.\n\nUse 'New Book' to create a new project.",
    "confirm_delete_chapter": "Remove \"{title}\" from chapters table?\n(The file on disk will NOT be deleted)",
    "cover_set_as": "Set as book cover",
    "cover_delete": "Delete this image from covers folder?",
    "importing_doc": "Importing manuscript…",
    "importing_wait": "Converting {dir}\nPlease wait…",
    "importing_book": "Importing book…",
    "importing_book_wait": "Unpacking {dir}\nPlease wait…",
    "import_failed": "Import failed",
    "sb_asterisks": "Asterisks (* * *)",
    "sb_fleuron": "Fleuron (curl)",
    "sb_lozenge": "Lozenge (diamond)",
    "sb_line": "Line with diamond",
    "order_saved": "Order saved",
    "loading_chapter": "Loading chapter…",
    "opened_time": "Opened {time}",
    "load_error": "Load error",
    "editing": "Editing…",
    "publish_ok": "Publish ok",
    "build_error": "Build error",
    "created_time": "Created {time}",
    "pick_docx_title": "Choose manuscript (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "Choose archive to convert to Markdown",
    "pick_vault_title": "Choose book folder (vault)",
    "pick_print_dir_title": "Folder where to save print.html and PDF",
    "pick_cover_title": "Choose cover file to copy to cover/ folder",
    "pick_np_dir_title": "Choose where to save book project",
    "enter_book_title": "Please enter a title for the book.",
    "select_project_folder": "Please select a folder where to save the project.",
    "enter_section_title": "Please enter a title for the section.",
    "move_to_trash_confirm": "Move \"{name}\" to trash (.cestina)?",
    "use_as_cover_confirm": "Use \"{name}\" as EPUB cover?\n\n(It will be copied to images/ and set in book.yaml)",
    "cover_epub_img_note": "For the EPUB cover, an image is required (JPEG/PNG). KDP recommends JPEG/TIFF with long side 2560px.",
    "project_creating_title": "Creating project…",
    "project_creating_wait": "Creating book folder and book.yaml…",
    "btn_pub_preview": "📖 Preview…",
    "btn_pub_preview_title": "Open publication preview (real EPUB and print PDF)",
    "pub_preview_title": "Publication Preview",
    "pub_target_print": "Print PDF",
    "pub_target_ebook": "Ebook",
    "btn_pub_update": "Update preview",
    "pub_status_none": "No preview",
    "pub_status_generating": "Generating…",
    "pub_status_current": "Updated",
    "pub_status_stale": "Needs update",
    "pub_status_error": "Error",
    "pub_download_file": "📥 Download file",
    "pub_bleed_label": "Bleed",
    "pub_view_label": "View:",
    "pub_view_single": "Single page",
    "pub_view_facing": "Facing pages",
    "pub_guides_label": "Guides:",
    "pub_guide_cut": "Trim",
    "pub_guide_safe": "Safe area",
    "pub_profile_label": "Profile:",
    "pub_profile_reader": "E-reader",
    "pub_profile_tablet": "Tablet",
    "pub_profile_phone": "Smartphone",
    "pub_orient_label": "Orientation:",
    "pub_orient_portrait": "Portrait",
    "pub_orient_landscape": "Landscape",
    "pub_text_size_label": "Text:",
    "pub_font_reset": "Reset",
    "pub_kindle_disclaimer": "Rendering may vary across readers. For Kindle, also verify with Kindle Previewer.",
    "pub_nav_jump_label": "Jump to:",
    "pub_nav_prev": "◀ Previous",
    "pub_nav_next": "Next ▶",
    "pub_zoom_label": "Zoom:",
    "pub_zoom_fit_width": "Fit width",
    "pub_zoom_fit_page": "Fit page",
    "pub_empty_title": "No preview generated",
    "pub_prompt_update": "Click 'Update preview' to compile and view the real document.",
    "pub_loading_desc": "Compiling exact layout and rendering generated byte stream.",
    "pub_error_title": "Preview generation error",
    "pub_page_indicator": "Page {p} of {total}",
    "pub_recto": "Recto (Right)",
    "pub_verso": "Verso (Left)",
    "tb_bold": "Bold (Cmd+B)",
    "tb_italic": "Italic (Cmd+I)",
    "tb_h2": "Subtitle (H2)",
    "tb_h3": "Section (H3)",
    "tb_quote": "Blockquote (>)",
    "tb_ul": "Bulleted list (-)",
    "tb_ol": "Numbered list (1.)",
    "tb_break": "Scene break (***)",
    "tb_link": "Insert link (Cmd+K)",
    "tb_image": "Insert image",
    "tb_table": "Insert table",
    "table_modal_title": "Insert Table",
    "table_cols": "Columns:",
    "table_rows": "Data rows:",
    "table_insert": "Insert",
    "table_col_prefix": "Column",
    "table_cell_prefix": "Cell",
    "welcome_version": "Version 1.0.0-alpha.1",
    "welcome_tutorial_link": "Show PubCraft Tutorial",
    "welcome_shortcuts_link": "Open Help & Shortcuts",
    "welcome_btn_new": "✨ New Book…",
    "welcome_btn_docx": "📄 Import Manuscript (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 Open Existing Folder…",
    "recents_header": "Recent Files",
    "recents_search_placeholder": "Filter projects…",
    "recents_workspace_label": "Workspace:",
    "recents_change_workspace": "Change…",
    "recents_all_tab": "All Recents",
    "recents_workspace_tab": "In Workspace",
    "recents_empty_title": "No recent books",
    "recents_empty_desc": "Create a new book or open a folder to start writing.",
    "recents_btn_open_other": "Open Other…",
    "recents_btn_open_selected": "Open Selected",
    "recents_remove_tooltip": "Remove from recents (does not delete files on disk)",
    "topbar_recents": "📚 Recent Files",
    "topbar_recents_title": "Open recent projects and launcher",
    "topbar_close_book": "✕ Close",
    "topbar_close_book_title": "Close current book",
    "fs_workspace": "⭐ Workspace",
    "fs_documents": "📄 Documents",
    "fs_desktop": "🖥️ Desktop",
    "fs_downloads": "📥 Downloads",
    "fs_filter_placeholder": "Filter folders…",
    "fs_set_workspace_btn": "📌 Set as Default Workspace",
    "fs_workspace_saved": "Workspace updated",
    "tutorial_modal_title": "PubCraft Guide & Tutorial",
    "tut_tab_guide": "Tutorial",
    "tut_tab_shortcuts": "Shortcuts",
    "topbar_title_tooltip": "Click to rename book",
    "notebook_header": "Notebook",
    "notebook_section_tooltip": "Notes and ideas for this book",
    "btn_add_note": "＋ New Note…",
    "btn_toggle_notebook": "📓 Notes",
    "btn_focus_mode": "Focus",
    "btn_focus_mode_title": "Distraction-Free Focus Mode (F11 or Esc to exit)",
    "focus_exit": "Exit",
    "focus_font_typewriter": "⌨️ Typewriter font",
    "focus_font_serif": "📖 Book Serif font",
    "focus_dim_off": "👁️ Focus: Off",
    "focus_dim_sentence": "🎯 Focus: Sentence",
    "focus_dim_paragraph": "📑 Focus: Paragraph",
    "focus_dim_title": "Cycle Focus Mode (Off / Sentence / Paragraph like iA Writer)",
    "focus_super_focus_title": "Toggle Super Focus (vertically centered text and continuous typewriter scrolling like iA Writer)",
    "focus_super_focus_on": "🎯 Super Focus: On",
    "focus_super_focus_off": "🎯 Super Focus: Off",
    "focus_select_chapter": "Select chapter",
    "theme_dark": "Dark",
    "theme_light": "Light",
    "theme_toggle_title": "Toggle Dark / Light theme (black background / white text)",
    "recents_prune_missing": "Clean missing",
    "recents_prune_title": "Remove missing project folders from recents",
    "recents_btn_clear_all": "Clear all",
    "recents_clear_confirm_title": "Clear recent projects",
    "recents_clear_confirm_desc": "Remove all recent projects from this list? (Your files on disk will not be deleted).",
    "recent_missing_badge": "Missing",
    "recent_missing_title": "Book not found",
    "recent_missing_desc": "The folder for this book no longer exists on disk:",
    "recent_missing_prompt": "Do you want to remove it from recent projects?",
    "recent_remove_btn": "Remove from recents",
    "notebook_drawer_title": "Book Notes",
    "btn_new_note": "New note",
    "note_title_ph": "Note title…",
    "note_content_ph": "Ideas, research, plot reminders, scenes to develop…",
    "note_insert_title": "Insert note text into current chapter at cursor",
    "note_insert": "Insert in Manuscript",
    "note_convert_title": "Turn this note into an official book chapter",
    "note_convert": "Convert to Chapter",
    "note_empty": "No notes yet",
    "note_untitled": "Untitled Note",
    "note_unsaved": "Unsaved",
    "note_loading": "Loading…",
    "note_del_title": "Delete Note",
    "note_del_confirm": "Delete note \"{title}\" from notebook?",
    "note_convert_confirm": "Convert this note into an official chapter of the manuscript? It will be added to the table of contents and book.yaml.",
    "note_inserted": "Inserted into manuscript!",
    "btn_delete": "Delete"
  },
  "it": {
    "chapter": "Capitolo",
    "cancel": "Annulla",
    "loading": "Caricamento…",

    "focus_exit_title": "Esci dalla modalità Non Disturbo (Esc)",
    "focus_toggle_font_title": "Alterna font: Macchina da scrivere / Serif",
    "focus_toggle_super_focus_title": "Alterna modalità Super Focus (scorrimento continuo al centro stile iA Writer)",
    "focus_select_chapter_title": "Seleziona capitolo",

    "btn_new_project": "✨ Nuovo libro…",
    "btn_new_project_title": "Crea un nuovo progetto libro (cartella e book.yaml)",
    "btn_open": "Apri…",
    "btn_open_title": "Apri una cartella libro esistente o importa un file",
    "btn_new_chapter": "＋ Nuovo capitolo",
    "btn_new_chapter_title": "Nuovo capitolo nel libro aperto",
    "btn_style": "Stile: ",
    "btn_style_title": "Stile del libro: vale per stampa, PDF e anteprima",
    "btn_opening": "Inizio: ",
    "btn_opening_title": "Inizio capitolo: capolettera, maiuscoletto o semplice",
    "btn_watch": "👁️ Live Watch",
    "btn_watch_title": "Apri anteprima di stampa live sincronizzata in tempo reale",
    "btn_kindle": "EPUB Kindle",
    "btn_kindle_title": "Genera l'EPUB per Kindle (KDP)",
    "btn_print": "PDF stampa…",
    "btn_print_title": "Genera print.html e PDF per la stampa",
    "save_ready": "Pronto",
    "save_saving": "Salvataggio…",
    "save_saved": "Salvato ",
    "save_error": "Errore salvataggio",
    "conflict_detected": "Conflitto rilevato",
    "save_failed": "Salvataggio non riuscito",
    "save_failed_msg": "Impossibile salvare il capitolo corrente prima di procedere.\nIl testo inserito è stato preservato.",
    "save_failed_change_ch": "Impossibile salvare il capitolo corrente. Il testo inserito è stato preservato.\nCorreggi l'errore o riprova prima di cambiare capitolo.",
    "btn_retry_save": "Riprova a Salvare",
    "btn_retry": "Riprova",
    "covers_header": "Copertine",
    "btn_add_cover": "＋ Aggiungi…",
    "untitled": "Senza titolo",
    "empty_title": "Inizia un nuovo libro",
    "empty_desc": "Puoi creare un nuovo progetto da zero oppure aprire una cartella esistente (vault Markdown).",
    "btn_create_new_empty": "✨ Crea nuovo libro…",
    "btn_open_folder_empty": "📁 Apri cartella esistente…",
    "words_count": "{count} parole",
    "words_short": "parole",
    "vw_editor": "Editor",
    "vw_split": "Editor + Anteprima",
    "vw_preview": "Anteprima",
    "editor_placeholder": "Scrivi qui… il salvataggio è automatico.",
    "open_modal_title": "Apri o crea libro",
    "open_choice_new": "✨ Crea un nuovo libro da zero",
    "open_choice_folder": "📁 Cartella libro esistente (vault markdown)",
    "open_choice_archive": "📕 Importa archivio di progetto → converti in Markdown",
    "open_choice_docx": "📄 Importa manoscritto (.docx, .vellum, .md) → converti in libro",
    "btn_cancel": "Annulla",
    "np_modal_title": "✨ Crea Nuovo Progetto Libro",
    "np_desc": "Verrà creata una cartella dedicata (vault Markdown) pronta per la scrittura, con book.yaml, capitolo iniziale e cartelle per copertine e immagini.",
    "np_book_title_label": "Titolo del libro *",
    "np_book_title_ph": "es. Il Segreto del Fiume",
    "np_author_label": "Nome autore",
    "np_author_ph": "es. Mario Rossi",
    "np_parent_dir_label": "Cartella genitore (dove salvare il progetto)",
    "np_parent_dir_ph": "Seleziona la cartella...",
    "btn_browse": "Sfoglia…",
    "np_folder_name_label": "Nome della cartella",
    "np_folder_name_ph": "es. il-segreto-del-fiume",
    "np_initial_style_label": "Stile editoriale iniziale",
    "btn_create_project": "Crea Progetto",
    "fs_modal_title": "Scegli…",
    "fs_home": "🏠 Home",
    "fs_volumes": "📂 Volumi",
    "fs_up": "↑ Su",
    "fs_new_folder": "Nuova cartella…",
    "fs_select": "Scegli",
    "fs_select_file": "Scegli file",
    "fs_select_folder": "Scegli cartella",
    "fs_select_files_count": "Scegli {n} file",
    "fs_open_folder": "📁 Apri questa cartella",
    "fs_new_folder_prompt": "Nome della nuova cartella:",
    "btn_report_issue": "Segnala un problema",
    "btn_report_issue_title": "Segnala un problema o visualizza la diagnostica",
    "report_modal_title": "🛠️ Segnala un problema",
    "report_privacy_notice": "Viene preparato un rapporto diagnostico locale con versione, ambiente ed errori registrati. Nessun testo di manoscritto, contenuto dei capitoli, PDF o dato personale viene allegato o inviato automaticamente.",
    "report_desc_label": "Descrivi cosa stavi facendo o cosa non funziona *",
    "report_desc_ph": "Es. Tentativo di esportare il PDF di stampa ma l'operazione è fallita...",
    "report_expected_label": "Risultato atteso (opzionale)",
    "report_observed_label": "Risultato osservato (opzionale)",
    "report_recent_errors_label": "Errori di sistema recenti registrati:",
    "report_no_recent_errors": "Nessun errore recente registrato.",
    "report_preview_title": "Anteprima del rapporto generato:",
    "report_btn_prepare": "Prepara rapporto",
    "report_btn_copy": "📋 Copia rapporto",
    "report_btn_export": "💾 Esporta su file…",
    "report_copied_msg": "Copiato negli appunti!",
    "empty_folder": "Cartella vuota",
    "print_modal_title": "PDF / stampa — parametri",
    "print_modal_desc": "Stile del libro (salvato nel vault, vale per stampa, PDF e anteprima):",
    "style_label": "Stile",
    "body_font_label": "Font del testo",
    "scene_break_label": "Interruzioni di scena",
    "opening_label": "Inizio capitolo",
    "opening_dropcap": "Capolettera",
    "opening_smallcaps": "Maiuscoletto (Adelphi/Penguin)",
    "opening_none": "Semplice a filo",
    "trim_label": "Formato carta",
    "pages_label": "Pagine stimate",
    "pages_est_ph": "≈ {pages} ({words} parole)",
    "bleed_label": "bordo pieno",
    "preview_chapter_label": "Capitolo anteprima:",
    "open_full_viewprint": "📖 Visualizza intero libro impaginato",
    "dest_folder_lbl_html": "Destinazione: <b>out-stampa/</b> (nella cartella del libro)",
    "dest_folder_custom": "Destinazione: {dir}",
    "btn_pick_other_dir": "Scegli altra cartella…",
    "kdp_proof_note": "La prova finale resta il KDP Print Previewer.",
    "btn_start_build": "Avvia build",
    "preview_no_text": "Nessun testo presente in questo capitolo.",
    "preview_empty": "Capitolo vuoto.",
    "no_chapters_yet": "Nessun capitolo",
    "new_chapter_title": "Aggiungi al libro",
    "nc_type_label": "Tipo di sezione",
    "nc_type_chapter": "📖 Capitolo standard",
    "nc_type_dedication": "📜 Dedica (Front matter)",
    "nc_type_epigraph": "✒️ Epigrafe (Front matter)",
    "nc_type_preface": "📑 Prefazione / Introduzione (Front matter)",
    "nc_type_acknowledgments": "🙏 Ringraziamenti (Back matter)",
    "nc_type_about": "👤 Sull'Autore (Back matter)",
    "nc_title_label": "Titolo",
    "nc_title_ph": "es. Capitolo 1",
    "nc_position_label": "Posizione",
    "nc_pos_after": "Subito dopo il capitolo selezionato",
    "nc_pos_end": "In fondo al libro",
    "nc_pos_start": "All'inizio del libro",
    "btn_add": "Aggiungi",
    "default_dedication": "Dedica",
    "default_epigraph": "Epigrafe",
    "default_about_author": "Sull'Autore",
    "default_acknowledgments": "Ringraziamenti",
    "default_preface": "Prefazione",
    "modal_close": "Chiudi",
    "modal_error": "Errore",
    "build_running_title": "Build in corso…",
    "build_running_desc": "Attendere, sto compilando con pubcraft…",
    "build_pdf_success": "PDF Creato con Successo ✔",
    "build_success": "Fatto ✔",
    "btn_open_pdf": "Apri PDF 📄",
    "btn_print_browser": "Stampa / Salva in PDF subito (Browser) 🖨",
    "btn_show_in_finder": "Mostra nel Finder",
    "btn_show_in_folder": "Mostra nella cartella",
    "unsaved_changes_title": "Modifiche non salvate",
    "no_book_found_title": "Nessun libro trovato",
    "no_book_found_desc": "La cartella selezionata non contiene file Markdown (.md) né un file book.yaml.\n\nUsa 'Nuovo libro' per creare un nuovo progetto.",
    "confirm_delete_chapter": "Rimuovere \"{title}\" dalla tabella dei capitoli?\n(Il file sul disco NON verrà cancellato)",
    "cover_set_as": "Imposta come copertina",
    "cover_delete": "Cancellare questa immagine dalla cartella copertine?",
    "importing_doc": "Importo il manoscritto…",
    "importing_wait": "Conversione di {dir}\nattendere…",
    "importing_book": "Importo il libro…",
    "importing_book_wait": "Scompattazione di {dir}\nattendere…",
    "import_failed": "Import fallito",
    "sb_asterisks": "Asterischi (* * *)",
    "sb_fleuron": "Fleuron (svirgolata)",
    "sb_lozenge": "Losanga",
    "sb_line": "Linea con rombo",
    "order_saved": "Ordine salvato",
    "loading_chapter": "Caricamento capitolo…",
    "opened_time": "Aperto {time}",
    "load_error": "Errore caricamento",
    "editing": "Modifica…",
    "publish_ok": "Publish ok",
    "build_error": "Errore build",
    "created_time": "Creato {time}",
    "pick_docx_title": "Scegli il manoscritto (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "Scegli l'archivio da convertire in markdown",
    "pick_vault_title": "Scegli la cartella del libro (vault)",
    "pick_print_dir_title": "Cartella dove salvare print.html e il PDF",
    "pick_cover_title": "Scegli il file di copertina da copiare nella cartella cover/",
    "pick_np_dir_title": "Scegli dove salvare il progetto libro",
    "enter_book_title": "Inserisci un titolo per il libro.",
    "select_project_folder": "Seleziona la cartella in cui salvare il progetto.",
    "enter_section_title": "Inserisci un titolo per la sezione.",
    "move_to_trash_confirm": "Spostare \"{name}\" in .cestina?",
    "use_as_cover_confirm": "Usare \"{name}\" come copertina dell'EPUB?\n\n(Viene copiata in images/ e impostata in book.yaml)",
    "cover_epub_img_note": "Per la copertina EPUB serve un'immagine (JPEG/PNG). KDP vuole JPEG/TIFF con lato lungo 2560px.",
    "project_creating_title": "Creazione progetto…",
    "project_creating_wait": "Creo la cartella del libro e book.yaml…",
    "btn_pub_preview": "📖 Anteprima…",
    "btn_pub_preview_title": "Apri l'anteprima di pubblicazione (EPUB e PDF reale)",
    "pub_preview_title": "Anteprima pubblicazione",
    "pub_target_print": "PDF stampa",
    "pub_target_ebook": "Ebook",
    "btn_pub_update": "Aggiorna anteprima",
    "pub_status_none": "Nessuna anteprima",
    "pub_status_generating": "Generazione in corso…",
    "pub_status_current": "Aggiornata",
    "pub_status_stale": "Da aggiornare",
    "pub_status_error": "Errore",
    "pub_download_file": "📥 Scarica file",
    "pub_bleed_label": "Abbondanza",
    "pub_view_label": "Vista:",
    "pub_view_single": "Pagina singola",
    "pub_view_facing": "Affiancate",
    "pub_guides_label": "Guide:",
    "pub_guide_cut": "Taglio",
    "pub_guide_safe": "Area sicura",
    "pub_profile_label": "Profilo:",
    "pub_profile_reader": "E-reader",
    "pub_profile_tablet": "Tablet",
    "pub_profile_phone": "Smartphone",
    "pub_orient_label": "Orientamento:",
    "pub_orient_portrait": "Verticale",
    "pub_orient_landscape": "Orizzontale",
    "pub_text_size_label": "Testo:",
    "pub_font_reset": "Ripristina",
    "pub_kindle_disclaimer": "La resa può variare secondo il lettore. Per Kindle verifica anche con Kindle Previewer.",
    "pub_nav_jump_label": "Vai a:",
    "pub_nav_prev": "◀ Precedente",
    "pub_nav_next": "Successiva ▶",
    "pub_zoom_label": "Zoom:",
    "pub_zoom_fit_width": "Adatta larghezza",
    "pub_zoom_fit_page": "Adatta pagina",
    "pub_empty_title": "Nessuna anteprima generata",
    "pub_prompt_update": "Premi 'Aggiorna anteprima' per compilare e visualizzare il documento reale.",
    "pub_loading_desc": "Compilazione dell'impaginazione reale e rendering fedele dei byte generati.",
    "pub_error_title": "Errore di generazione anteprima",
    "pub_page_indicator": "Pagina {p} di {total}",
    "pub_recto": "Recto (Destra)",
    "pub_verso": "Verso (Sinistra)",
    "tb_bold": "Grassetto (Cmd+B)",
    "tb_italic": "Corsivo (Cmd+I)",
    "tb_h2": "Sottotitolo (H2)",
    "tb_h3": "Sezione (H3)",
    "tb_quote": "Citazione (>)",
    "tb_ul": "Elenco puntato (-)",
    "tb_ol": "Elenco numerato (1.)",
    "tb_break": "Interruzione di scena (***)",
    "tb_link": "Inserisci link (Cmd+K)",
    "tb_image": "Inserisci immagine",
    "tb_table": "Inserisci tabella",
    "table_modal_title": "Inserisci Tabella",
    "table_cols": "Colonne:",
    "table_rows": "Righe di dati:",
    "table_insert": "Inserisci",
    "table_col_prefix": "Colonna",
    "table_cell_prefix": "Cella",
    "welcome_version": "Versione 1.0.0-alpha.1",
    "welcome_tutorial_link": "Mostra introduzione a PubCraft",
    "welcome_shortcuts_link": "Guida e scorciatoie da tastiera",
    "welcome_btn_new": "✨ Nuovo Libro…",
    "welcome_btn_docx": "📄 Importa manoscritto (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 Sfoglia altre cartelle…",
    "recents_header": "Progetti recenti",
    "recents_search_placeholder": "Cerca tra i progetti…",
    "recents_workspace_label": "Area di lavoro:",
    "recents_change_workspace": "Cambia…",
    "recents_all_tab": "Tutti i recenti",
    "recents_workspace_tab": "Nell'area di lavoro",
    "recents_empty_title": "Nessun progetto recente",
    "recents_empty_desc": "Crea un nuovo libro o apri una cartella per iniziare.",
    "recents_btn_open_other": "Sfoglia altre cartelle…",
    "recents_btn_open_selected": "Apri selezionato",
    "recents_remove_tooltip": "Rimuovi dai recenti (non elimina i file dal disco)",
    "topbar_recents": "📚 Progetti recenti",
    "topbar_recents_title": "Apri i progetti recenti e la schermata iniziale",
    "topbar_close_book": "✕ Chiudi libro",
    "topbar_close_book_title": "Chiudi il libro corrente",
    "fs_workspace": "⭐ Area di lavoro",
    "fs_documents": "📄 Documenti",
    "fs_desktop": "🖥️ Scrivania",
    "fs_downloads": "📥 Download",
    "fs_filter_placeholder": "Filtra cartelle…",
    "fs_set_workspace_btn": "📌 Imposta come area di lavoro predefinita",
    "fs_workspace_saved": "Area di lavoro aggiornata",
    "tutorial_modal_title": "Guida e Tutorial di PubCraft",
    "tut_tab_guide": "Guida",
    "tut_tab_shortcuts": "Scorciatoie",
    "topbar_title_tooltip": "Clicca per rinominare il libro",
    "notebook_header": "Taccuino",
    "notebook_section_tooltip": "Appunti e idee legati a questo libro",
    "btn_add_note": "＋ Nuova nota…",
    "btn_toggle_notebook": "📓 Note",
    "btn_focus_mode": "Non disturbo",
    "btn_focus_mode_title": "Modalità Non Disturbo / Scrittura a tutto schermo (F11 o Esc per uscire)",
    "focus_exit": "Esci",
    "focus_font_typewriter": "⌨️ Macchina da scrivere",
    "focus_font_serif": "📖 Carattere Serif",
    "focus_dim_off": "👁️ Focus: Spento",
    "focus_dim_sentence": "🎯 Focus: Frase",
    "focus_dim_paragraph": "📑 Focus: Paragrafo",
    "focus_dim_title": "Alterna modalità Focus (Spento / Frase / Paragrafo stile iA Writer)",
    "focus_super_focus_title": "Attiva/disattiva modalità Super Focus (testo centrato in verticale e scorrimento continuo stile iA Writer)",
    "focus_super_focus_on": "🎯 Super Focus: Attivo",
    "focus_super_focus_off": "🎯 Super Focus: Disattivo",
    "focus_select_chapter": "Seleziona capitolo",
    "theme_dark": "Scuro",
    "theme_light": "Chiaro",
    "theme_toggle_title": "Alterna tema Scuro / Chiaro (sfondo nero e testo bianco)",
    "recents_prune_missing": "Pulisci non trovati",
    "recents_prune_title": "Rimuove dall'elenco i progetti le cui cartelle non esistono più sul disco",
    "recents_btn_clear_all": "Svuota elenco",
    "recents_clear_confirm_title": "Svuota elenco recenti",
    "recents_clear_confirm_desc": "Vuoi rimuovere tutti i progetti recenti da questo elenco? (I tuoi file sul disco non verranno toccati).",
    "recent_missing_badge": "Non trovato",
    "recent_missing_title": "Libro non trovato",
    "recent_missing_desc": "La cartella di questo libro non esiste più sul disco:",
    "recent_missing_prompt": "Vuoi rimuoverlo dall'elenco dei recenti?",
    "recent_remove_btn": "Rimuovi dall'elenco",
    "notebook_drawer_title": "Taccuino del libro",
    "btn_new_note": "Nuova nota",
    "note_title_ph": "Titolo appunto…",
    "note_content_ph": "Idee, ricerche, passaggi da ricordare, scene da sviluppare…",
    "note_insert_title": "Inserisci il testo della nota nel capitolo corrente al cursore",
    "note_insert": "Inserisci nel manoscritto",
    "note_convert_title": "Trasforma questa nota in un capitolo ufficiale del libro",
    "note_convert": "Converti in capitolo",
    "note_empty": "Nessun appunto presente",
    "note_untitled": "Nota senza titolo",
    "note_unsaved": "Non salvato",
    "note_loading": "Caricamento…",
    "note_del_title": "Elimina appunto",
    "note_del_confirm": "Eliminare la nota \"{title}\" dal taccuino?",
    "note_convert_confirm": "Vuoi trasformare questa nota in un capitolo ufficiale del libro? Verrà aggiunta all'indice del manoscritto e in book.yaml.",
    "note_inserted": "Inserito nel capitolo!",
    "btn_delete": "Elimina"
  },
  "es": {
    "chapter": "Capítulo",
    "cancel": "Cancelar",
    "loading": "Cargando…",

    "focus_exit_title": "Salir del modo sin distracciones (Esc)",
    "focus_toggle_font_title": "Alternar fuente: Máquina de escribir / Serif",
    "focus_dim_off": "👁️ Foco: Desactivado",
    "focus_dim_sentence": "🎯 Foco: Frase",
    "focus_dim_paragraph": "📑 Foco: Párrafo",
    "focus_dim_title": "Alternar modo Foco (Desactivado / Frase / Párrafo estilo iA Writer)",
    "focus_super_focus_title": "Alternar Super Foco (desplazamiento vertical centrado estilo iA Writer)",
    "focus_toggle_super_focus_title": "Alternar modo Super Foco (desplazamiento continuo al centro)",
    "focus_super_focus_on": "🎯 Super Foco: Activado",
    "focus_super_focus_off": "🎯 Super Foco: Desactivado",
    "focus_select_chapter": "Seleccionar capítulo",
    "focus_select_chapter_title": "Seleccionar capítulo",
    "recents_prune_missing": "Limpiar no encontrados",
    "recents_prune_title": "Eliminar proyectos cuyas carpetas ya no existen en el disco",
    "recents_btn_clear_all": "Vaciar lista",
    "recents_clear_confirm_title": "Vaciar lista de recientes",
    "recents_clear_confirm_desc": "¿Deseas eliminar todos los proyectos recientes de esta lista? (Tus archivos en el disco no se eliminarán).",
    "recent_missing_badge": "No encontrado",
    "recent_missing_title": "Libro no encontrado",
    "recent_missing_desc": "La carpeta de este libro ya no existe en el disco:",
    "recent_missing_prompt": "¿Deseas eliminarlo de la lista de recientes?",
    "recent_remove_btn": "Eliminar de recientes",

    "btn_new_project": "✨ Nuevo libro…",
    "btn_new_project_title": "Crear un nuevo proyecto de libro (carpeta y book.yaml)",
    "btn_open": "Abrir…",
    "btn_open_title": "Abrir una carpeta de libro existente o importar un archivo",
    "btn_new_chapter": "＋ Nuevo capítulo",
    "btn_new_chapter_title": "Nuevo capítulo en el libro abierto",
    "btn_style": "Estilo: ",
    "btn_style_title": "Estilo del libro: válido para impresión, PDF y vista previa",
    "btn_opening": "Inicio: ",
    "btn_opening_title": "Inicio de capítulo: letra capital, versalitas o simple",
    "btn_watch": "👁️ Live Watch",
    "btn_watch_title": "Abrir vista previa de impresión sincronizada en tiempo real",
    "btn_kindle": "Kindle EPUB",
    "btn_kindle_title": "Generar EPUB para Kindle (KDP)",
    "btn_print": "Imprimir / PDF…",
    "btn_print_title": "Generar print.html y PDF para impresión",
    "save_ready": "Listo",
    "save_saving": "Guardando…",
    "save_saved": "Guardado ",
    "save_error": "Error al guardar",
    "conflict_detected": "Conflicto detectado",
    "save_failed": "Error al guardar",
    "save_failed_msg": "No se pudo guardar el capítulo actual antes de continuar.\nEl texto introducido se ha conservado.",
    "save_failed_change_ch": "No se pudo guardar el capítulo actual. El texto introducido se ha conservado.\nCorrige el error o reintenta antes de cambiar de capítulo.",
    "btn_retry_save": "Reintentar Guardar",
    "btn_retry": "Reintentar",
    "covers_header": "Portadas",
    "btn_add_cover": "＋ Añadir…",
    "untitled": "Sin título",
    "empty_title": "Comenzar un nuevo libro",
    "empty_desc": "Puedes crear un nuevo proyecto desde cero o abrir una carpeta existente (bóveda Markdown).",
    "btn_create_new_empty": "✨ Crear nuevo libro…",
    "btn_open_folder_empty": "📁 Abrir carpeta existente…",
    "words_count": "{count} palabras",
    "words_short": "palabras",
    "vw_editor": "Editor",
    "vw_split": "Editor + Vista previa",
    "vw_preview": "Vista previa",
    "editor_placeholder": "Escribe aquí… el guardado es automático.",
    "open_modal_title": "Abrir o crear libro",
    "open_choice_new": "✨ Crear un nuevo libro desde cero",
    "open_choice_folder": "📁 Carpeta de libro existente (Markdown vault)",
    "open_choice_archive": "📕 Importar archivo de proyecto → convertir a Markdown",
    "open_choice_docx": "📄 Importar manuscrito (.docx, .vellum, .md) → convertir a libro",
    "btn_cancel": "Cancelar",
    "np_modal_title": "✨ Crear Nuevo Proyecto de Libro",
    "np_desc": "Se creará una carpeta dedicada (Markdown vault) lista para escribir, con book.yaml, capítulo inicial y carpetas para portadas e imágenes.",
    "np_book_title_label": "Título del libro *",
    "np_book_title_ph": "ej. El Secreto del Río",
    "np_author_label": "Nombre del autor",
    "np_author_ph": "ej. Juan Pérez",
    "np_parent_dir_label": "Carpeta superior (dónde guardar el proyecto)",
    "np_parent_dir_ph": "Seleccionar carpeta...",
    "btn_browse": "Examinar…",
    "np_folder_name_label": "Nombre de la carpeta",
    "np_folder_name_ph": "ej. el-secreto-del-rio",
    "np_initial_style_label": "Estilo editorial inicial",
    "btn_create_project": "Crear Proyecto",
    "fs_modal_title": "Elegir…",
    "fs_home": "🏠 Inicio",
    "fs_volumes": "📂 Volúmenes",
    "fs_up": "↑ Arriba",
    "fs_new_folder": "Nueva carpeta…",
    "fs_select": "Elegir",
    "fs_select_file": "Elegir archivo",
    "fs_select_folder": "Elegir carpeta",
    "fs_select_files_count": "Elegir {n} archivos",
    "fs_open_folder": "📁 Abrir esta carpeta",
    "fs_new_folder_prompt": "Nombre de la nueva carpeta:",
    "btn_report_issue": "Reportar un problema",
    "btn_report_issue_title": "Reportar un problema o ver diagnósticos",
    "report_modal_title": "🛠️ Reportar un problema",
    "report_privacy_notice": "Se prepara un informe de diagnóstico local con versión, entorno y errores registrados. No se adjuntan ni envían automáticamente manuscritos, capítulos, PDF ni datos personales.",
    "report_desc_label": "Describe qué estabas haciendo o qué no funciona *",
    "report_desc_ph": "Ej. Intenté exportar el PDF para imprimir pero la operación falló...",
    "report_expected_label": "Resultado esperado (opcional)",
    "report_observed_label": "Resultado observado (opcional)",
    "report_recent_errors_label": "Errores recientes del sistema registrados:",
    "report_no_recent_errors": "No hay errores recientes registrados.",
    "report_preview_title": "Vista previa del informe generado:",
    "report_btn_prepare": "Preparar informe",
    "report_btn_copy": "📋 Copiar informe",
    "report_btn_export": "💾 Exportar a archivo…",
    "report_copied_msg": "¡Copiado al portapapeles!",
    "empty_folder": "Carpeta vacía",
    "print_modal_title": "PDF / Impresión — Parámetros",
    "print_modal_desc": "Estilo del libro (guardado en el vault, válido para impresión, PDF y vista previa):",
    "style_label": "Estilo",
    "body_font_label": "Fuente del texto",
    "scene_break_label": "Separadores de escena",
    "opening_label": "Inicio de capítulo",
    "opening_dropcap": "Letra capital",
    "opening_smallcaps": "Versalitas (Adelphi/Penguin)",
    "opening_none": "Simple alineado",
    "trim_label": "Tamaño de página",
    "pages_label": "Páginas estimadas",
    "pages_est_ph": "≈ {pages} ({words} palabras)",
    "bleed_label": "sangrado completo",
    "preview_chapter_label": "Capítulo de vista previa:",
    "open_full_viewprint": "📖 Ver libro completo maquetado",
    "dest_folder_lbl_html": "Destino: <b>out-stampa/</b> (dentro de la carpeta del libro)",
    "dest_folder_custom": "Destino: {dir}",
    "btn_pick_other_dir": "Elegir otra carpeta…",
    "kdp_proof_note": "La prueba final sigue siendo KDP Print Previewer.",
    "btn_start_build": "Iniciar compilación",
    "preview_no_text": "Aún no hay texto en este capítulo.",
    "preview_empty": "Este capítulo está vacío.",
    "no_chapters_yet": "Sin capítulos",
    "new_chapter_title": "Añadir al libro",
    "nc_type_label": "Tipo de sección",
    "nc_type_chapter": "📖 Capítulo estándar",
    "nc_type_dedication": "📜 Dedicatoria (Front matter)",
    "nc_type_epigraph": "✒️ Epígrafe (Front matter)",
    "nc_type_preface": "📑 Prefacio / Introducción (Front matter)",
    "nc_type_acknowledgments": "🙏 Agradecimientos (Back matter)",
    "nc_type_about": "👤 Sobre el Autor (Back matter)",
    "nc_title_label": "Título",
    "nc_title_ph": "ej. Capítulo 1",
    "nc_position_label": "Posición",
    "nc_pos_after": "Inmediatamente después del capítulo seleccionado",
    "nc_pos_end": "Al final del libro",
    "nc_pos_start": "Al principio del libro",
    "btn_add": "Añadir",
    "default_dedication": "Dedicatoria",
    "default_epigraph": "Epígrafe",
    "default_about_author": "Sobre el Autor",
    "default_acknowledgments": "Agradecimientos",
    "default_preface": "Prefacio",
    "modal_close": "Cerrar",
    "modal_error": "Error",
    "build_running_title": "Compilación en curso…",
    "build_running_desc": "Por favor espere, compilando con pubcraft…",
    "build_pdf_success": "PDF creado con éxito ✔",
    "build_success": "Listo ✔",
    "btn_open_pdf": "Abrir PDF 📄",
    "btn_print_browser": "Imprimir / Guardar como PDF ahora (Navegador) 🖨",
    "btn_show_in_finder": "Mostrar en Finder",
    "btn_show_in_folder": "Mostrar en carpeta",
    "unsaved_changes_title": "Cambios no guardados",
    "no_book_found_title": "No se encontró ningún libro",
    "no_book_found_desc": "La carpeta seleccionada no contiene archivos Markdown (.md) ni un archivo book.yaml.\n\nUsa 'Nuevo libro' para crear un nuevo proyecto.",
    "confirm_delete_chapter": "¿Eliminar \"{title}\" de la tabla de capítulos?\n(El archivo en el disco NO se eliminará)",
    "cover_set_as": "Establecer como portada",
    "cover_delete": "¿Eliminar esta imagen de la carpeta de portadas?",
    "importing_doc": "Importando manuscrito…",
    "importing_wait": "Convirtiendo {dir}\nPor favor espere…",
    "importing_book": "Importando libro…",
    "importing_book_wait": "Descomprimiendo {dir}\nPor favor espere…",
    "import_failed": "Error al importar",
    "sb_asterisks": "Asteriscos (* * *)",
    "sb_fleuron": "Florón (adornillo)",
    "sb_lozenge": "Rombo",
    "sb_line": "Línea con rombo",
    "order_saved": "Orden guardado",
    "loading_chapter": "Cargando capítulo…",
    "opened_time": "Abierto {time}",
    "load_error": "Error de carga",
    "editing": "Modificando…",
    "publish_ok": "Publicado con éxito",
    "build_error": "Error de compilación",
    "created_time": "Creado {time}",
    "pick_docx_title": "Elige el manuscrito (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "Elige el archivo comprimido para convertir a Markdown",
    "pick_vault_title": "Elige la carpeta del libro (vault)",
    "pick_print_dir_title": "Carpeta donde guardar print.html y el PDF",
    "pick_cover_title": "Elige el archivo de portada a copiar en cover/",
    "pick_np_dir_title": "Elige dónde guardar el proyecto de libro",
    "enter_book_title": "Por favor, introduce un título para el libro.",
    "select_project_folder": "Por favor, selecciona una carpeta donde guardar el proyecto.",
    "enter_section_title": "Por favor, introduce un título para la sección.",
    "move_to_trash_confirm": "¿Mover \"{name}\" a la papelera (.cestina)?",
    "use_as_cover_confirm": "¿Usar \"{name}\" como portada del EPUB?\n\n(Se copiará a images/ y se registrará en book.yaml)",
    "cover_epub_img_note": "Para la portada EPUB se requiere una imagen (JPEG/PNG). KDP recomienda JPEG/TIFF con lado largo 2560px.",
    "project_creating_title": "Creando proyecto…",
    "project_creating_wait": "Creando carpeta del libro y book.yaml…",
    "btn_pub_preview": "📖 Vista previa…",
    "btn_pub_preview_title": "Abrir vista previa de publicación (EPUB y PDF real)",
    "pub_preview_title": "Vista previa de publicación",
    "pub_target_print": "PDF impresión",
    "pub_target_ebook": "Ebook",
    "btn_pub_update": "Actualizar vista previa",
    "pub_status_none": "Sin vista previa",
    "pub_status_generating": "Generando…",
    "pub_status_current": "Actualizada",
    "pub_status_stale": "Por actualizar",
    "pub_status_error": "Error",
    "pub_download_file": "📥 Descargar archivo",
    "pub_bleed_label": "Sangrado",
    "pub_view_label": "Vista:",
    "pub_view_single": "Página simple",
    "pub_view_facing": "Páginas opuestas",
    "pub_guides_label": "Guías:",
    "pub_guide_cut": "Corte",
    "pub_guide_safe": "Área segura",
    "pub_profile_label": "Perfil:",
    "pub_profile_reader": "Lector electrónico",
    "pub_profile_tablet": "Tableta",
    "pub_profile_phone": "Smartphone",
    "pub_orient_label": "Orientación:",
    "pub_orient_portrait": "Vertical",
    "pub_orient_landscape": "Horizontal",
    "pub_text_size_label": "Texto:",
    "pub_font_reset": "Restablecer",
    "pub_kindle_disclaimer": "El resultado puede variar según el lector. Para Kindle comprueba también con Kindle Previewer.",
    "pub_nav_jump_label": "Ir a:",
    "pub_nav_prev": "◀ Anterior",
    "pub_nav_next": "Siguiente ▶",
    "pub_zoom_label": "Zoom:",
    "pub_zoom_fit_width": "Ajustar al ancho",
    "pub_zoom_fit_page": "Ajustar página",
    "pub_empty_title": "Sin vista previa generada",
    "pub_prompt_update": "Haz clic en 'Actualizar vista previa' para compilar y ver el documento real.",
    "pub_loading_desc": "Compilando maquetación real y renderizando los bytes generados.",
    "pub_error_title": "Error al generar vista previa",
    "pub_page_indicator": "Página {p} de {total}",
    "pub_recto": "Recto (Derecha)",
    "pub_verso": "Verso (Izquierda)",
    "tb_bold": "Negrita (Cmd+B)",
    "tb_italic": "Cursiva (Cmd+I)",
    "tb_h2": "Subtítulo (H2)",
    "tb_h3": "Sección (H3)",
    "tb_quote": "Cita (>)",
    "tb_ul": "Lista con viñetas (-)",
    "tb_ol": "Lista numerada (1.)",
    "tb_break": "Salto de escena (***)",
    "tb_link": "Insertar enlace (Cmd+K)",
    "tb_image": "Insertar imagen",
    "tb_table": "Insertar tabla",
    "table_modal_title": "Insertar Tabla",
    "table_cols": "Columnas:",
    "table_rows": "Filas de datos:",
    "table_insert": "Insertar",
    "table_col_prefix": "Columna",
    "table_cell_prefix": "Celda",
    "welcome_version": "Versión 1.0.0-alpha.1",
    "welcome_tutorial_link": "Mostrar tutorial de PubCraft",
    "welcome_shortcuts_link": "Ayuda y atajos de teclado",
    "welcome_btn_new": "✨ Nuevo Libro…",
    "welcome_btn_docx": "📄 Importar manuscrito (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 Abrir carpeta existente…",
    "recents_header": "Archivos recientes",
    "recents_search_placeholder": "Filtrar proyectos…",
    "recents_workspace_label": "Espacio de trabajo:",
    "recents_change_workspace": "Cambiar…",
    "recents_all_tab": "Todos los recientes",
    "recents_workspace_tab": "En espacio de trabajo",
    "recents_empty_title": "No hay libros recientes",
    "recents_empty_desc": "Crea un nuevo libro o abre una carpeta para comenzar.",
    "recents_btn_open_other": "Abrir otra…",
    "recents_btn_open_selected": "Abrir seleccionado",
    "recents_remove_tooltip": "Quitar de recientes (no elimina archivos del disco)",
    "topbar_recents": "📚 Proyectos recientes",
    "topbar_recents_title": "Abrir proyectos recientes e inicio",
    "topbar_close_book": "✕ Cerrar libro",
    "topbar_close_book_title": "Cerrar libro actual",
    "fs_workspace": "⭐ Espacio de trabajo",
    "fs_documents": "📄 Documentos",
    "fs_desktop": "🖥️ Escritorio",
    "fs_downloads": "📥 Descargas",
    "fs_filter_placeholder": "Filtrar carpetas…",
    "fs_set_workspace_btn": "📌 Establecer como espacio de trabajo",
    "fs_workspace_saved": "Espacio de trabajo actualizado",
    "tutorial_modal_title": "Guía y Tutorial de PubCraft",
    "tut_tab_guide": "Tutorial",
    "tut_tab_shortcuts": "Atajos",
    "topbar_title_tooltip": "Haz clic para renombrar el libro",
    "notebook_header": "Cuaderno",
    "notebook_section_tooltip": "Notas e ideas para este libro",
    "btn_add_note": "＋ Nueva nota…",
    "btn_toggle_notebook": "📓 Notas",
    "btn_focus_mode": "Sin distracciones",
    "btn_focus_mode_title": "Modo sin distracciones / Pantalla completa (F11 o Esc para salir)",
    "focus_exit": "Salir",
    "focus_font_typewriter": "⌨️ Máquina de escribir",
    "focus_font_serif": "📖 Fuente Serif",
    "theme_dark": "Oscuro",
    "theme_light": "Claro",
    "theme_toggle_title": "Alternar modo Oscuro / Claro",
    "notebook_drawer_title": "Cuaderno del libro",
    "btn_new_note": "Nueva nota",
    "note_title_ph": "Título de la nota…",
    "note_content_ph": "Ideas, investigaciones, recordatorios, escenas a desarrollar…",
    "note_insert_title": "Insertar el texto de la nota en el capítulo actual en el cursor",
    "note_insert": "Insertar en el manuscrito",
    "note_convert_title": "Convertir esta nota en un capítulo oficial del libro",
    "note_convert": "Convertir en capítulo",
    "note_empty": "No hay notas aún",
    "note_untitled": "Nota sin título",
    "note_unsaved": "Sin guardar",
    "note_loading": "Cargando…",
    "note_del_title": "Eliminar nota",
    "note_del_confirm": "¿Eliminar la nota \"{title}\" del cuaderno?",
    "note_convert_confirm": "¿Convertir esta nota en un capítulo oficial del libro? Se añadirá al índice y a book.yaml.",
    "note_inserted": "¡Insertado en el capítulo!",
    "btn_delete": "Eliminar"
  },
  "fr": {
    "chapter": "Chapitre",
    "cancel": "Annuler",
    "loading": "Chargement…",

    "focus_exit_title": "Quitter le mode sans distraction (Échap)",
    "focus_toggle_font_title": "Changer la police: Machine à écrire / Sérif",
    "focus_dim_off": "👁️ Focus: Désactivé",
    "focus_dim_sentence": "🎯 Focus: Phrase",
    "focus_dim_paragraph": "📑 Focus: Paragraphe",
    "focus_dim_title": "Changer le mode Focus (Désactivé / Phrase / Paragraphe style iA Writer)",
    "focus_super_focus_title": "Activer/désactiver Super Focus (défilement centré style iA Writer)",
    "focus_toggle_super_focus_title": "Activer/désactiver mode Super Focus (défilement centré)",
    "focus_super_focus_on": "🎯 Super Focus: Activé",
    "focus_super_focus_off": "🎯 Super Focus: Désactivé",
    "focus_select_chapter": "Sélectionner un chapitre",
    "focus_select_chapter_title": "Sélectionner un chapitre",
    "recents_prune_missing": "Nettoyer les introuvables",
    "recents_prune_title": "Supprimer les projets dont les dossiers n'existent plus sur le disque",
    "recents_btn_clear_all": "Tout effacer",
    "recents_clear_confirm_title": "Effacer la liste des récents",
    "recents_clear_confirm_desc": "Voulez-vous supprimer tous les projets récents de cette liste ? (Vos fichiers sur le disque ne seront pas supprimés).",
    "recent_missing_badge": "Introuvable",
    "recent_missing_title": "Livre introuvable",
    "recent_missing_desc": "Le dossier de ce livre n'existe plus sur le disque :",
    "recent_missing_prompt": "Voulez-vous le retirer de la liste des récents ?",
    "recent_remove_btn": "Retirer des récents",

    "btn_new_project": "✨ Nouveau livre…",
    "btn_new_project_title": "Créer un nouveau projet de livre (dossier et book.yaml)",
    "btn_open": "Ouvrir…",
    "btn_open_title": "Ouvrir un dossier de livre existant ou importer un fichier",
    "btn_new_chapter": "＋ Nouveau chapitre",
    "btn_new_chapter_title": "Nouveau chapitre dans le livre ouvert",
    "btn_style": "Style : ",
    "btn_style_title": "Style du livre : s'applique à l'impression, au PDF et à l'aperçu",
    "btn_opening": "Début : ",
    "btn_opening_title": "Début de chapitre : lettrine, petites capitales ou simple",
    "btn_watch": "👁️ Live Watch",
    "btn_watch_title": "Ouvrir l'aperçu d'impression synchronisé en temps réel",
    "btn_kindle": "Kindle EPUB",
    "btn_kindle_title": "Générer l'EPUB pour Kindle (KDP)",
    "btn_print": "Imprimer / PDF…",
    "btn_print_title": "Générer print.html et PDF pour impression",
    "save_ready": "Prêt",
    "save_saving": "Enregistrement…",
    "save_saved": "Enregistré ",
    "save_error": "Erreur d'enregistrement",
    "conflict_detected": "Conflit détecté",
    "save_failed": "Échec de l'enregistrement",
    "save_failed_msg": "Impossible d'enregistrer le chapitre en cours avant de continuer.\nVotre texte a été préservé.",
    "save_failed_change_ch": "Impossible d'enregistrer le chapitre en cours. Votre texte a été préservé.\nVeuillez corriger l'erreur avant de changer de chapitre.",
    "btn_retry_save": "Réessayer l'enregistrement",
    "btn_retry": "Réessayer",
    "covers_header": "Couvertures",
    "btn_add_cover": "＋ Ajouter…",
    "untitled": "Sans titre",
    "empty_title": "Commencer un nouveau livre",
    "empty_desc": "Vous pouvez créer un nouveau projet à partir de zéro ou ouvrir un dossier existant (coffre Markdown).",
    "btn_create_new_empty": "✨ Créer un nouveau livre…",
    "btn_open_folder_empty": "📁 Ouvrir dossier existant…",
    "words_count": "{count} mots",
    "words_short": "mots",
    "vw_editor": "Éditeur",
    "vw_split": "Éditeur + Aperçu",
    "vw_preview": "Aperçu",
    "editor_placeholder": "Écrivez ici… l'enregistrement est automatique.",
    "open_modal_title": "Ouvrir ou créer un livre",
    "open_choice_new": "✨ Créer un nouveau livre à partir de zéro",
    "open_choice_folder": "📁 Dossier de livre existant (Markdown vault)",
    "open_choice_archive": "📕 Importer une archive de projet → convertir en Markdown",
    "open_choice_docx": "📄 Importer manuscrit (.docx, .vellum, .md) → convertir en livre",
    "btn_cancel": "Annuler",
    "np_modal_title": "✨ Créer un Nouveau Projet de Livre",
    "np_desc": "Un dossier dédié (Markdown vault) sera créé prêt pour l'écriture, avec book.yaml, chapitre initial et dossiers couvertures et images.",
    "np_book_title_label": "Titre du livre *",
    "np_book_title_ph": "ex. Le Secret de la Rivière",
    "np_author_label": "Nom de l'auteur",
    "np_author_ph": "ex. Jean Dupont",
    "np_parent_dir_label": "Dossier parent (où enregistrer le projet)",
    "np_parent_dir_ph": "Sélectionner un dossier...",
    "btn_browse": "Parcourir…",
    "np_folder_name_label": "Nom du dossier",
    "np_folder_name_ph": "ex. le-secret-de-la-riviere",
    "np_initial_style_label": "Style éditorial initial",
    "btn_create_project": "Créer le Projet",
    "fs_modal_title": "Choisir…",
    "fs_home": "🏠 Accueil",
    "fs_volumes": "📂 Volumes",
    "fs_up": "↑ Haut",
    "fs_new_folder": "Nouveau dossier…",
    "fs_select": "Choisir",
    "fs_select_file": "Choisir le fichier",
    "fs_select_folder": "Choisir le dossier",
    "fs_select_files_count": "Choisir {n} fichiers",
    "fs_open_folder": "📁 Ouvrir ce dossier",
    "fs_new_folder_prompt": "Nom du nouveau dossier :",
    "btn_report_issue": "Signaler un problème",
    "btn_report_issue_title": "Signaler un problème ou afficher les diagnostics",
    "report_modal_title": "🛠️ Signaler un problème",
    "report_privacy_notice": "Un rapport de diagnostic local est préparé avec la version, l'environnement et les erreurs enregistrées. Aucun manuscrit, extrait, PDF ou donnée personnelle n'est joint ou envoyé automatiquement.",
    "report_desc_label": "Décrivez ce que vous faisiez ou ce qui ne fonctionne pas *",
    "report_desc_ph": "Ex. J'ai essayé d'exporter le PDF d'impression mais l'opération a échoué...",
    "report_expected_label": "Résultat attendu (optionnel)",
    "report_observed_label": "Résultat observé (optionnel)",
    "report_recent_errors_label": "Erreurs système récentes enregistrées :",
    "report_no_recent_errors": "Aucune erreur récente enregistrée.",
    "report_preview_title": "Aperçu du rapport généré :",
    "report_btn_prepare": "Préparer le rapport",
    "report_btn_copy": "📋 Copier le rapport",
    "report_btn_export": "💾 Exporter vers un fichier…",
    "report_copied_msg": "Copié dans le presse-papiers !",
    "empty_folder": "Dossier vide",
    "print_modal_title": "PDF / Impression — Paramètres",
    "print_modal_desc": "Style du livre (enregistré dans le vault, s'applique à l'impression, au PDF et à l'aperçu) :",
    "style_label": "Style",
    "body_font_label": "Police du texte",
    "scene_break_label": "Séparateurs de scène",
    "opening_label": "Début de chapitre",
    "opening_dropcap": "Lettrine",
    "opening_smallcaps": "Petites capitales (Adelphi/Penguin)",
    "opening_none": "Simple sans retrait",
    "trim_label": "Format de page",
    "pages_label": "Pages estimées",
    "pages_est_ph": "≈ {pages} ({words} mots)",
    "bleed_label": "fond perdu",
    "preview_chapter_label": "Chapitre en aperçu :",
    "open_full_viewprint": "📖 Voir le livre complet mis en page",
    "dest_folder_lbl_html": "Destination : <b>out-stampa/</b> (dans le dossier du livre)",
    "dest_folder_custom": "Destination : {dir}",
    "btn_pick_other_dir": "Choisir un autre dossier…",
    "kdp_proof_note": "L'épreuve finale reste le KDP Print Previewer.",
    "btn_start_build": "Lancer la compilation",
    "preview_no_text": "Aucun texte dans ce chapitre pour le moment.",
    "preview_empty": "Ce chapitre est vide.",
    "no_chapters_yet": "Aucun chapitre",
    "new_chapter_title": "Ajouter au livre",
    "nc_type_label": "Type de section",
    "nc_type_chapter": "📖 Chapitre standard",
    "nc_type_dedication": "📜 Dédicace (Front matter)",
    "nc_type_epigraph": "✒️ Épigraphe (Front matter)",
    "nc_type_preface": "📑 Préface / Introduction (Front matter)",
    "nc_type_acknowledgments": "🙏 Remerciements (Back matter)",
    "nc_type_about": "👤 À propos de l'auteur (Back matter)",
    "nc_title_label": "Titre",
    "nc_title_ph": "ex. Chapitre 1",
    "nc_position_label": "Position",
    "nc_pos_after": "Juste après le chapitre sélectionné",
    "nc_pos_end": "À la fin du livre",
    "nc_pos_start": "Au début du livre",
    "btn_add": "Ajouter",
    "default_dedication": "Dédicace",
    "default_epigraph": "Épigraphe",
    "default_about_author": "À propos de l'auteur",
    "default_acknowledgments": "Remerciements",
    "default_preface": "Préface",
    "modal_close": "Fermer",
    "modal_error": "Erreur",
    "build_running_title": "Compilation en cours…",
    "build_running_desc": "Veuillez patienter, compilation avec pubcraft…",
    "build_pdf_success": "PDF créé avec succès ✔",
    "build_success": "Terminé ✔",
    "btn_open_pdf": "Ouvrir PDF 📄",
    "btn_print_browser": "Imprimer / Enregistrer en PDF maintenant (Navigateur) 🖨",
    "btn_show_in_finder": "Afficher dans le Finder",
    "btn_show_in_folder": "Afficher dans le dossier",
    "unsaved_changes_title": "Modifications non enregistrées",
    "no_book_found_title": "Aucun livre trouvé",
    "no_book_found_desc": "Le dossier sélectionné ne contient pas de fichiers Markdown (.md) ni de fichier book.yaml.\n\nUtilisez 'Nouveau livre' pour créer un projet.",
    "confirm_delete_chapter": "Supprimer \"{title}\" de la table des matières ?\n(Le fichier sur le disque ne sera PAS supprimé)",
    "cover_set_as": "Définir comme couverture",
    "cover_delete": "Supprimer cette image du dossier des couvertures ?",
    "importing_doc": "Importation du manuscrit…",
    "importing_wait": "Conversion de {dir}\nVeuillez patienter…",
    "importing_book": "Importation du livre…",
    "importing_book_wait": "Extraction de {dir}\nVeuillez patienter…",
    "import_failed": "Échec de l'importation",
    "sb_asterisks": "Astérisques (* * *)",
    "sb_fleuron": "Fleuron (arabesque)",
    "sb_lozenge": "Losange",
    "sb_line": "Ligne avec losange",
    "order_saved": "Ordre enregistré",
    "loading_chapter": "Chargement du chapitre…",
    "opened_time": "Ouvert à {time}",
    "load_error": "Erreur de chargement",
    "editing": "Modification…",
    "publish_ok": "Publication réussie",
    "build_error": "Erreur de compilation",
    "created_time": "Créé à {time}",
    "pick_docx_title": "Choisissez le manuscrit (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "Choisissez l'archive à convertir en Markdown",
    "pick_vault_title": "Choisissez le dossier du livre (vault)",
    "pick_print_dir_title": "Dossier où enregistrer print.html et le PDF",
    "pick_cover_title": "Choisissez le fichier de couverture à copier dans cover/",
    "pick_np_dir_title": "Choisissez où enregistrer le projet de livre",
    "enter_book_title": "Veuillez saisir un titre pour le livre.",
    "select_project_folder": "Veuillez sélectionner un dossier pour le projet.",
    "enter_section_title": "Veuillez saisir un titre pour la section.",
    "move_to_trash_confirm": "Déplacer \"{name}\" vers la corbeille (.cestina) ?",
    "use_as_cover_confirm": "Utiliser \"{name}\" comme couverture EPUB ?\n\n(L'image sera copiée dans images/ et inscrite dans book.yaml)",
    "cover_epub_img_note": "Une image (JPEG/PNG) est requise pour la couverture EPUB. KDP recommande JPEG/TIFF de 2560px de haut.",
    "project_creating_title": "Création du projet…",
    "project_creating_wait": "Création du dossier et de book.yaml…",
    "btn_pub_preview": "📖 Aperçu…",
    "btn_pub_preview_title": "Ouvrir l'aperçu de publication (EPUB et PDF réel)",
    "pub_preview_title": "Aperçu de publication",
    "pub_target_print": "PDF impression",
    "pub_target_ebook": "Ebook",
    "btn_pub_update": "Mettre à jour l'aperçu",
    "pub_status_none": "Aucun aperçu",
    "pub_status_generating": "Génération en cours…",
    "pub_status_current": "À jour",
    "pub_status_stale": "À actualiser",
    "pub_status_error": "Erreur",
    "pub_download_file": "📥 Télécharger le fichier",
    "pub_bleed_label": "Fond perdu",
    "pub_view_label": "Affichage :",
    "pub_view_single": "Page simple",
    "pub_view_facing": "Pages en vis-à-vis",
    "pub_guides_label": "Repères :",
    "pub_guide_cut": "Coupe",
    "pub_guide_safe": "Zone tranquille",
    "pub_profile_label": "Profil :",
    "pub_profile_reader": "Liseuse",
    "pub_profile_tablet": "Tablette",
    "pub_profile_phone": "Smartphone",
    "pub_orient_label": "Orientation :",
    "pub_orient_portrait": "Portrait",
    "pub_orient_landscape": "Paysage",
    "pub_text_size_label": "Texte :",
    "pub_font_reset": "Réinitialiser",
    "pub_kindle_disclaimer": "Le rendu peut varier selon la liseuse. Pour Kindle, vérifiez aussi avec Kindle Previewer.",
    "pub_nav_jump_label": "Aller à :",
    "pub_nav_prev": "◀ Précédente",
    "pub_nav_next": "Suivante ▶",
    "pub_zoom_label": "Zoom :",
    "pub_zoom_fit_width": "Ajuster à la largeur",
    "pub_zoom_fit_page": "Ajuster à la page",
    "pub_empty_title": "Aucun aperçu généré",
    "pub_prompt_update": "Cliquez sur 'Mettre à jour l'aperçu' pour compiler et visualiser le document réel.",
    "pub_loading_desc": "Compilation de la mise en page réelle et rendu fidèle des octets générés.",
    "pub_error_title": "Erreur de génération d'aperçu",
    "pub_page_indicator": "Page {p} sur {total}",
    "pub_recto": "Recto (Droite)",
    "pub_verso": "Verso (Gauche)",
    "tb_bold": "Gras (Cmd+B)",
    "tb_italic": "Italique (Cmd+I)",
    "tb_h2": "Sous-titre (H2)",
    "tb_h3": "Section (H3)",
    "tb_quote": "Citation (>)",
    "tb_ul": "Liste à puces (-)",
    "tb_ol": "Liste numérotée (1.)",
    "tb_break": "Saut de scène (***)",
    "tb_link": "Insérer un lien (Cmd+K)",
    "tb_image": "Insérer une image",
    "tb_table": "Insérer un tableau",
    "table_modal_title": "Insérer un Tableau",
    "table_cols": "Colonnes :",
    "table_rows": "Lignes de données :",
    "table_insert": "Insérer",
    "table_col_prefix": "Colonne",
    "table_cell_prefix": "Cellule",
    "welcome_version": "Version 1.0.0-alpha.1",
    "welcome_tutorial_link": "Afficher le didacticiel PubCraft",
    "welcome_shortcuts_link": "Aide et raccourcis clavier",
    "welcome_btn_new": "✨ Nouveau Livre…",
    "welcome_btn_docx": "📄 Importer manuscrit (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 Ouvrir dossier existant…",
    "recents_header": "Fichiers récents",
    "recents_search_placeholder": "Filtrer les projets…",
    "recents_workspace_label": "Espace de travail :",
    "recents_change_workspace": "Changer…",
    "recents_all_tab": "Tous les récents",
    "recents_workspace_tab": "Dans l'espace de travail",
    "recents_empty_title": "Aucun livre récent",
    "recents_empty_desc": "Créez un nouveau livre ou ouvrez un dossier pour commencer.",
    "recents_btn_open_other": "Ouvrir un autre…",
    "recents_btn_open_selected": "Ouvrir la sélection",
    "recents_remove_tooltip": "Retirer des récents (ne supprime aucun fichier sur le disque)",
    "topbar_recents": "📚 Projets récents",
    "topbar_recents_title": "Ouvrir les projets récents et le lanceur",
    "topbar_close_book": "✕ Fermer le livre",
    "topbar_close_book_title": "Fermer le livre en cours",
    "fs_workspace": "⭐ Espace de travail",
    "fs_documents": "📄 Documents",
    "fs_desktop": "🖥️ Bureau",
    "fs_downloads": "📥 Téléchargements",
    "fs_filter_placeholder": "Filtrer les dossiers…",
    "fs_set_workspace_btn": "📌 Définir comme espace de travail par défaut",
    "fs_workspace_saved": "Espace de travail mis à jour",
    "tutorial_modal_title": "Guide et Didacticiel PubCraft",
    "tut_tab_guide": "Didacticiel",
    "tut_tab_shortcuts": "Raccourcis",
    "topbar_title_tooltip": "Cliquez pour renommer le livre",
    "notebook_header": "Carnet",
    "notebook_section_tooltip": "Notes et idées pour ce livre",
    "btn_add_note": "＋ Nouvelle note…",
    "btn_toggle_notebook": "📓 Notes",
    "btn_focus_mode": "Sans distraction",
    "btn_focus_mode_title": "Mode sans distraction / Plein écran (F11 ou Échap pour quitter)",
    "focus_exit": "Quitter",
    "focus_font_typewriter": "⌨️ Machine à écrire",
    "focus_font_serif": "📖 Police Serif",
    "theme_dark": "Sombre",
    "theme_light": "Clair",
    "theme_toggle_title": "Basculer entre thème Sombre et Clair",
    "notebook_drawer_title": "Carnet du livre",
    "btn_new_note": "Nouvelle note",
    "note_title_ph": "Titre de la note…",
    "note_content_ph": "Idées, recherches, rappels d'intrigue, scènes à développer…",
    "note_insert_title": "Insérer le texte de la note dans le chapitre actuel au curseur",
    "note_insert": "Insérer dans le manuscrit",
    "note_convert_title": "Transformer cette note en chapitre officiel du livre",
    "note_convert": "Convertir en chapitre",
    "note_empty": "Aucune note pour l'instant",
    "note_untitled": "Note sans titre",
    "note_unsaved": "Non enregistré",
    "note_loading": "Chargement…",
    "note_del_title": "Supprimer la note",
    "note_del_confirm": "Supprimer la note \"{title}\" du carnet ?",
    "note_convert_confirm": "Convertir cette note en chapitre officiel du livre ? Elle sera ajoutée à la table des matières et à book.yaml.",
    "note_inserted": "Inséré dans le chapitre !",
    "btn_delete": "Supprimer"
  },
  "de": {
    "chapter": "Kapitel",
    "cancel": "Abbrechen",
    "loading": "Laden…",

    "focus_exit_title": "Fokusmodus beenden (Esc)",
    "focus_toggle_font_title": "Schrift umschalten: Schreibmaschine / Serif",
    "focus_dim_off": "👁️ Fokus: Aus",
    "focus_dim_sentence": "🎯 Fokus: Satz",
    "focus_dim_paragraph": "📑 Fokus: Absatz",
    "focus_dim_title": "Fokusmodus wechseln (Aus / Satz / Absatz wie iA Writer)",
    "focus_super_focus_title": "Super-Fokus umschalten (vertikal zentriertes Scrollen wie iA Writer)",
    "focus_toggle_super_focus_title": "Super-Fokus umschalten (zentriertes Schreibmaschinenscrollen)",
    "focus_super_focus_on": "🎯 Super-Fokus: An",
    "focus_super_focus_off": "🎯 Super-Fokus: Aus",
    "focus_select_chapter": "Kapitel auswählen",
    "focus_select_chapter_title": "Kapitel auswählen",
    "recents_prune_missing": "Fehlende bereinigen",
    "recents_prune_title": "Projekte entfernen, deren Ordner auf der Festplatte nicht mehr existieren",
    "recents_btn_clear_all": "Alle löschen",
    "recents_clear_confirm_title": "Zuletzt geöffnete Projekte leeren",
    "recents_clear_confirm_desc": "Möchten Sie alle zuletzt geöffneten Projekte aus dieser Liste entfernen? (Ihre Dateien auf der Festplatte werden nicht gelöscht).",
    "recent_missing_badge": "Nicht gefunden",
    "recent_missing_title": "Buch nicht gefunden",
    "recent_missing_desc": "Der Ordner für dieses Buch existiert nicht mehr auf der Festplatte:",
    "recent_missing_prompt": "Möchten Sie es aus den letzten Projekten entfernen?",
    "recent_remove_btn": "Aus Liste entfernen",

    "btn_new_project": "✨ Neues Buch…",
    "btn_new_project_title": "Neues Buchprojekt erstellen (Ordner und book.yaml)",
    "btn_open": "Öffnen…",
    "btn_open_title": "Vorhandenen Buchordner öffnen oder Datei importieren",
    "btn_new_chapter": "＋ Neues Kapitel",
    "btn_new_chapter_title": "Neues Kapitel im geöffneten Buch",
    "btn_style": "Stil: ",
    "btn_style_title": "Buchstil: gilt für Druck, PDF und Vorschau",
    "btn_opening": "Anfang: ",
    "btn_opening_title": "Kapitelanfang: Initiale, Kapitälchen oder einfach",
    "btn_watch": "👁️ Live Watch",
    "btn_watch_title": "Echtzeit-Druckvorschau öffnen",
    "btn_kindle": "Kindle EPUB",
    "btn_kindle_title": "EPUB für Kindle (KDP) generieren",
    "btn_print": "Drucken / PDF…",
    "btn_print_title": "print.html und PDF für den Druck generieren",
    "save_ready": "Bereit",
    "save_saving": "Speichern…",
    "save_saved": "Gespeichert ",
    "save_error": "Speicherfehler",
    "conflict_detected": "Konflikt erkannt",
    "save_failed": "Speichern fehlgeschlagen",
    "save_failed_msg": "Das aktuelle Kapitel konnte nicht gespeichert werden.\nIhr Text wurde erhalten.",
    "save_failed_change_ch": "Das aktuelle Kapitel konnte nicht gespeichert werden. Ihr Text wurde gesichert.\nBitte beheben Sie den Fehler vor dem Kapitelwechsel.",
    "btn_retry_save": "Erneut speichern",
    "btn_retry": "Wiederholen",
    "covers_header": "Cover",
    "btn_add_cover": "＋ Hinzufügen…",
    "untitled": "Ohne Titel",
    "empty_title": "Ein neues Buch beginnen",
    "empty_desc": "Erstellen Sie ein neues Projekt oder öffnen Sie einen vorhandenen Ordner (Markdown-Vault).",
    "btn_create_new_empty": "✨ Neues Buch erstellen…",
    "btn_open_folder_empty": "📁 Vorhandenen Ordner öffnen…",
    "words_count": "{count} Wörter",
    "words_short": "Wörter",
    "vw_editor": "Editor",
    "vw_split": "Editor + Vorschau",
    "vw_preview": "Vorschau",
    "editor_placeholder": "Hier schreiben… automatisches Speichern ist aktiv.",
    "open_modal_title": "Buch öffnen oder erstellen",
    "open_choice_new": "✨ Ein neues Buch von Grund auf neu erstellen",
    "open_choice_folder": "📁 Vorhandener Buchordner (Markdown vault)",
    "open_choice_archive": "📕 Projektarchiv importieren → in Markdown konvertieren",
    "open_choice_docx": "📄 Manuskript (.docx, .vellum, .md) → in Buch umwandeln",
    "btn_cancel": "Abbrechen",
    "np_modal_title": "✨ Neues Buchprojekt Erstellen",
    "np_desc": "Ein eigener Ordner (Markdown vault) wird für das Buch vorbereitet, mit book.yaml, erstem Kapitel und Ordnern für Cover und Bilder.",
    "np_book_title_label": "Buchtitel *",
    "np_book_title_ph": "z.B. Das Geheimnis des Flusses",
    "np_author_label": "Autorenname",
    "np_author_ph": "z.B. Max Mustermann",
    "np_parent_dir_label": "Übergeordneter Ordner (Speicherort)",
    "np_parent_dir_ph": "Ordner auswählen...",
    "btn_browse": "Durchsuchen…",
    "np_folder_name_label": "Ordnername",
    "np_folder_name_ph": "z.B. das-geheimnis-des-flusses",
    "np_initial_style_label": "Anfänglicher Buchstil",
    "btn_create_project": "Projekt Erstellen",
    "fs_modal_title": "Auswählen…",
    "fs_home": "🏠 Home",
    "fs_volumes": "📂 Laufwerke",
    "fs_up": "↑ Nach oben",
    "fs_new_folder": "Neuer Ordner…",
    "fs_select": "Auswählen",
    "fs_select_file": "Datei auswählen",
    "fs_select_folder": "Ordner auswählen",
    "fs_select_files_count": "{n} Dateien auswählen",
    "fs_open_folder": "📁 Diesen Ordner öffnen",
    "fs_new_folder_prompt": "Name des neuen Ordners:",
    "btn_report_issue": "Problem melden",
    "btn_report_issue_title": "Problem melden oder Diagnose anzeigen",
    "report_modal_title": "🛠️ Problem melden",
    "report_privacy_notice": "Ein lokaler Diagnosebericht wird mit Version, Umgebung und Fehlern erstellt. Es werden keine Manuskripte, Kapitelinhalte, PDFs oder persönlichen Daten automatisch angehängt oder gesendet.",
    "report_desc_label": "Beschreiben Sie, was Sie getan haben oder was nicht funktioniert *",
    "report_desc_ph": "Z. B. Wollte Druck-PDF exportieren, aber der Vorgang ist fehlgeschlagen...",
    "report_expected_label": "Erwartetes Ergebnis (optional)",
    "report_observed_label": "Tatsächliches Ergebnis (optional)",
    "report_recent_errors_label": "Kürzlich aufgetretene Systemfehler:",
    "report_no_recent_errors": "Keine kürzlichen Fehler aufgezeichnet.",
    "report_preview_title": "Vorschau des generierten Berichts:",
    "report_btn_prepare": "Bericht erstellen",
    "report_btn_copy": "📋 Bericht kopieren",
    "report_btn_export": "💾 In Datei exportieren…",
    "report_copied_msg": "In die Zwischenablage kopiert!",
    "empty_folder": "Leerer Ordner",
    "print_modal_title": "PDF / Druck — Einstellungen",
    "print_modal_desc": "Buchstil (im Vault gespeichert, gilt für Druck, PDF und Vorschau):",
    "style_label": "Stil",
    "body_font_label": "Textschriftart",
    "scene_break_label": "Szenentrenner",
    "opening_label": "Kapitelanfang",
    "opening_dropcap": "Initiale",
    "opening_smallcaps": "Kapitälchen (Adelphi/Penguin)",
    "opening_none": "Einfach bündig",
    "trim_label": "Buchformat",
    "pages_label": "Geschätzte Seiten",
    "pages_est_ph": "≈ {pages} ({words} Wörter)",
    "bleed_label": "Anschnitt (Bleed)",
    "preview_chapter_label": "Vorschau-Kapitel:",
    "open_full_viewprint": "📖 Vollständig gesetztes Buch ansehen",
    "dest_folder_lbl_html": "Zielordner: <b>out-stampa/</b> (im Buchordner)",
    "dest_folder_custom": "Zielordner: {dir}",
    "btn_pick_other_dir": "Anderen Ordner wählen…",
    "kdp_proof_note": "Die endgültige Prüfung bleibt der KDP Print Previewer.",
    "btn_start_build": "Build starten",
    "preview_no_text": "In diesem Kapitel ist noch kein Text vorhanden.",
    "preview_empty": "Dieses Kapitel ist leer.",
    "no_chapters_yet": "Keine Kapitel",
    "new_chapter_title": "Dem Buch hinzufügen",
    "nc_type_label": "Abschnittstyp",
    "nc_type_chapter": "📖 Standardkapitel",
    "nc_type_dedication": "📜 Widmung (Front matter)",
    "nc_type_epigraph": "✒️ Epigraph (Front matter)",
    "nc_type_preface": "📑 Vorwort / Einführung (Front matter)",
    "nc_type_acknowledgments": "🙏 Danksagung (Back matter)",
    "nc_type_about": "👤 Über den Autor (Back matter)",
    "nc_title_label": "Titel",
    "nc_title_ph": "z.B. Kapitel 1",
    "nc_position_label": "Position",
    "nc_pos_after": "Direkt nach dem ausgewählten Kapitel",
    "nc_pos_end": "Am Ende des Buches",
    "nc_pos_start": "Am Anfang des Buches",
    "btn_add": "Hinzufügen",
    "default_dedication": "Widmung",
    "default_epigraph": "Epigraph",
    "default_about_author": "Über den Autor",
    "default_acknowledgments": "Danksagung",
    "default_preface": "Vorwort",
    "modal_close": "Schließen",
    "modal_error": "Fehler",
    "build_running_title": "Build läuft…",
    "build_running_desc": "Bitte warten, kompiliere mit pubcraft…",
    "build_pdf_success": "PDF erfolgreich erstellt ✔",
    "build_success": "Fertig ✔",
    "btn_open_pdf": "PDF öffnen 📄",
    "btn_print_browser": "Jetzt drucken / als PDF speichern (Browser) 🖨",
    "btn_show_in_finder": "Im Finder anzeigen",
    "btn_show_in_folder": "Im Ordner anzeigen",
    "unsaved_changes_title": "Ungespeicherte Änderungen",
    "no_book_found_title": "Kein Buch gefunden",
    "no_book_found_desc": "Der gewählte Ordner enthält keine Markdown-Dateien (.md) oder book.yaml.\n\nVerwenden Sie 'Neues Buch', um ein neues Projekt zu erstellen.",
    "confirm_delete_chapter": "\"{title}\" aus der Kapitelliste entfernen?\n(Die Datei auf der Festplatte wird NICHT gelöscht)",
    "cover_set_as": "Als Cover festlegen",
    "cover_delete": "Dieses Bild aus dem Cover-Ordner löschen?",
    "importing_doc": "Manuskript wird importiert…",
    "importing_wait": "Konvertiere {dir}\nBitte warten…",
    "importing_book": "Buch wird importiert…",
    "importing_book_wait": "Entpacke {dir}\nBitte warten…",
    "import_failed": "Import fehlgeschlagen",
    "sb_asterisks": "Sternchen (* * *)",
    "sb_fleuron": "Fleuron (Zierrat)",
    "sb_lozenge": "Raute",
    "sb_line": "Linie mit Raute",
    "order_saved": "Reihenfolge gespeichert",
    "loading_chapter": "Kapitel wird geladen…",
    "opened_time": "Geöffnet um {time}",
    "load_error": "Fehler beim Laden",
    "editing": "Bearbeiten…",
    "publish_ok": "Veröffentlichung erfolgreich",
    "build_error": "Build-Fehler",
    "created_time": "Erstellt um {time}",
    "pick_docx_title": "Wählen Sie das Manuskript (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "Wählen Sie das Archiv zur Markdown-Konvertierung",
    "pick_vault_title": "Wählen Sie den Buchordner (Vault)",
    "pick_print_dir_title": "Ordner für print.html und das PDF",
    "pick_cover_title": "Cover-Datei auswählen zum Kopieren in cover/",
    "pick_np_dir_title": "Speicherort für das Buchprojekt wählen",
    "enter_book_title": "Bitte geben Sie einen Buchtitel ein.",
    "select_project_folder": "Bitte wählen Sie einen Ordner für das Projekt.",
    "enter_section_title": "Bitte geben Sie einen Titel für den Abschnitt ein.",
    "move_to_trash_confirm": "\"{name}\" in den Papierkorb (.cestina) verschieben?",
    "use_as_cover_confirm": "\"{name}\" als EPUB-Cover verwenden?\n\n(Wird nach images/ kopiert und in book.yaml festgelegt)",
    "cover_epub_img_note": "Für das EPUB-Cover wird ein Bild (JPEG/PNG) benötigt. KDP empfiehlt JPEG/TIFF mit langer Kante 2560px.",
    "project_creating_title": "Projekt wird erstellt…",
    "project_creating_wait": "Buchordner und book.yaml werden angelegt…",
    "btn_pub_preview": "📖 Vorschau…",
    "btn_pub_preview_title": "Veröffentlichungsvorschau öffnen (EPUB und echtes PDF)",
    "pub_preview_title": "Veröffentlichungsvorschau",
    "pub_target_print": "Druck-PDF",
    "pub_target_ebook": "E-Book",
    "btn_pub_update": "Vorschau aktualisieren",
    "pub_status_none": "Keine Vorschau",
    "pub_status_generating": "Wird generiert…",
    "pub_status_current": "Aktuell",
    "pub_status_stale": "Zu aktualisieren",
    "pub_status_error": "Fehler",
    "pub_download_file": "📥 Datei herunterladen",
    "pub_bleed_label": "Beschnitt",
    "pub_view_label": "Ansicht:",
    "pub_view_single": "Einzelseite",
    "pub_view_facing": "Doppelseite",
    "pub_guides_label": "Hilfslinien:",
    "pub_guide_cut": "Beschnittkante",
    "pub_guide_safe": "Sicherheitsbereich",
    "pub_profile_label": "Profil:",
    "pub_profile_reader": "E-Reader",
    "pub_profile_tablet": "Tablet",
    "pub_profile_phone": "Smartphone",
    "pub_orient_label": "Ausrichtung:",
    "pub_orient_portrait": "Hochformat",
    "pub_orient_landscape": "Querformat",
    "pub_text_size_label": "Text:",
    "pub_font_reset": "Zurücksetzen",
    "pub_kindle_disclaimer": "Die Darstellung kann je nach Lesegerät variieren. Für Kindle bitte auch mit Kindle Previewer prüfen.",
    "pub_nav_jump_label": "Gehe zu:",
    "pub_nav_prev": "◀ Zurück",
    "pub_nav_next": "Weiter ▶",
    "pub_zoom_label": "Zoom:",
    "pub_zoom_fit_width": "Seitenbreite",
    "pub_zoom_fit_page": "Ganze Seite",
    "pub_empty_title": "Noch keine Vorschau generiert",
    "pub_prompt_update": "Klicken Sie auf 'Vorschau aktualisieren', um das echte Dokument zu kompilieren.",
    "pub_loading_desc": "Echtes Layout wird berechnet und gerendert.",
    "pub_error_title": "Fehler bei der Vorschau-Erstellung",
    "pub_page_indicator": "Seite {p} von {total}",
    "pub_recto": "Rechts (Recto)",
    "pub_verso": "Links (Verso)",
    "tb_bold": "Fett (Cmd+B)",
    "tb_italic": "Kursiv (Cmd+I)",
    "tb_h2": "Untertitel (H2)",
    "tb_h3": "Abschnitt (H3)",
    "tb_quote": "Zitat (>)",
    "tb_ul": "Aufzählungsliste (-)",
    "tb_ol": "Nummerierte Liste (1.)",
    "tb_break": "Szenenwechsel (***)",
    "tb_link": "Link einfügen (Cmd+K)",
    "tb_image": "Bild einfügen",
    "tb_table": "Tabelle einfügen",
    "table_modal_title": "Tabelle einfügen",
    "table_cols": "Spalten:",
    "table_rows": "Datenzeilen:",
    "table_insert": "Einfügen",
    "table_col_prefix": "Spalte",
    "table_cell_prefix": "Zelle",
    "welcome_version": "Version 1.0.0-alpha.1",
    "welcome_tutorial_link": "PubCraft-Tutorial anzeigen",
    "welcome_shortcuts_link": "Hilfe & Tastaturkürzel",
    "welcome_btn_new": "✨ Neues Buch…",
    "welcome_btn_docx": "📄 Manuskript importieren (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 Vorhandenen Ordner öffnen…",
    "recents_header": "Zuletzt verwendete Dateien",
    "recents_search_placeholder": "Projekte filtern…",
    "recents_workspace_label": "Arbeitsbereich:",
    "recents_change_workspace": "Ändern…",
    "recents_all_tab": "Alle zuletzt verwendeten",
    "recents_workspace_tab": "Im Arbeitsbereich",
    "recents_empty_title": "Keine zuletzt geöffneten Bücher",
    "recents_empty_desc": "Erstellen Sie ein neues Buch oder öffnen Sie einen Ordner.",
    "recents_btn_open_other": "Andere öffnen…",
    "recents_btn_open_selected": "Auswahl öffnen",
    "recents_remove_tooltip": "Aus Verlauf entfernen (löscht keine Dateien von der Festplatte)",
    "topbar_recents": "📚 Zuletzt verwendete Projekte",
    "topbar_recents_title": "Zuletzt geöffnete Bücher anzeigen",
    "topbar_close_book": "✕ Buch schließen",
    "topbar_close_book_title": "Aktuelles Buch schließen",
    "fs_workspace": "⭐ Arbeitsbereich",
    "fs_documents": "📄 Dokumente",
    "fs_desktop": "🖥️ Schreibtisch",
    "fs_downloads": "📥 Downloads",
    "fs_filter_placeholder": "Ordner filtern…",
    "fs_set_workspace_btn": "📌 Als Standard-Arbeitsbereich festlegen",
    "fs_workspace_saved": "Arbeitsbereich aktualisiert",
    "tutorial_modal_title": "PubCraft Anleitung & Tutorial",
    "tut_tab_guide": "Tutorial",
    "tut_tab_shortcuts": "Tastaturkürzel",
    "topbar_title_tooltip": "Klicken, um das Buch umzubenennen",
    "notebook_header": "Notizbuch",
    "notebook_section_tooltip": "Notizen und Ideen für dieses Buch",
    "btn_add_note": "＋ Neue Notiz…",
    "btn_toggle_notebook": "📓 Notizen",
    "btn_focus_mode": "Fokusmodus",
    "btn_focus_mode_title": "Ablenkungsfreier Fokusmodus / Vollbild (F11 oder Esc zum Beenden)",
    "focus_exit": "Beenden",
    "focus_font_typewriter": "⌨️ Schreibmaschine",
    "focus_font_serif": "📖 Serifenschrift",
    "theme_dark": "Dunkel",
    "theme_light": "Hell",
    "theme_toggle_title": "Dunkel- / Hellmodus umschalten",
    "notebook_drawer_title": "Buch-Notizbuch",
    "btn_new_note": "Neue Notiz",
    "note_title_ph": "Notiztitel…",
    "note_content_ph": "Ideen, Recherchen, Handlungserinnerungen, Szenen zum Entwickeln…",
    "note_insert_title": "Notiztext am Cursor in das aktuelle Kapitel einfügen",
    "note_insert": "In Manuskript einfügen",
    "note_convert_title": "Diese Notiz in ein offizielles Buchkapitel umwandeln",
    "note_convert": "In Kapitel umwandeln",
    "note_empty": "Noch keine Notizen",
    "note_untitled": "Unbenannte Notiz",
    "note_unsaved": "Nicht gespeichert",
    "note_loading": "Laden…",
    "note_del_title": "Notiz löschen",
    "note_del_confirm": "Notiz \"{title}\" aus dem Notizbuch löschen?",
    "note_convert_confirm": "Diese Notiz in ein offizielles Buchkapitel umwandeln? Sie wird dem Inhaltsverzeichnis und book.yaml hinzugefügt.",
    "note_inserted": "In das Kapitel eingefügt!",
    "btn_delete": "Löschen"
  },
  "pt": {
    "chapter": "Capítulo",
    "cancel": "Cancelar",
    "loading": "Carregando…",

    "focus_exit_title": "Sair do modo de foco (Esc)",
    "focus_toggle_font_title": "Alternar fonte: Máquina de escrever / Serif",
    "focus_dim_off": "👁️ Foco: Desativado",
    "focus_dim_sentence": "🎯 Foco: Frase",
    "focus_dim_paragraph": "📑 Foco: Parágrafo",
    "focus_dim_title": "Alternar modo Foco (Desativado / Frase / Parágrafo estilo iA Writer)",
    "focus_super_focus_title": "Alternar Super Foco (rolagem contínua centralizada estilo iA Writer)",
    "focus_toggle_super_focus_title": "Alternar modo Super Foco (rolagem contínua centralizada)",
    "focus_super_focus_on": "🎯 Super Foco: Ativado",
    "focus_super_focus_off": "🎯 Super Foco: Desativado",
    "focus_select_chapter": "Selecionar capítulo",
    "focus_select_chapter_title": "Selecionar capítulo",
    "recents_prune_missing": "Limpar não encontrados",
    "recents_prune_title": "Remover projetos cujas pastas não existem mais no disco",
    "recents_btn_clear_all": "Limpar tudo",
    "recents_clear_confirm_title": "Limpar projetos recentes",
    "recents_clear_confirm_desc": "Deseja remover todos os projetos recentes desta lista? (Seus arquivos no disco não serão excluídos).",
    "recent_missing_badge": "Não encontrado",
    "recent_missing_title": "Livro não encontrado",
    "recent_missing_desc": "A pasta deste livro não existe mais no disco:",
    "recent_missing_prompt": "Deseja removê-lo dos projetos recentes?",
    "recent_remove_btn": "Remover dos recentes",

    "btn_new_project": "✨ Novo livro…",
    "btn_new_project_title": "Criar um novo projeto de livro (pasta e book.yaml)",
    "btn_open": "Abrir…",
    "btn_open_title": "Abrir uma pasta de livro existente ou importar um arquivo",
    "btn_new_chapter": "＋ Novo capítulo",
    "btn_new_chapter_title": "Novo capítulo no livro aberto",
    "btn_style": "Estilo: ",
    "btn_style_title": "Estilo do livro: aplica-se à impressão, PDF e pré-visualização",
    "btn_opening": "Início: ",
    "btn_opening_title": "Início do capítulo: capitular, versaletes ou simples",
    "btn_watch": "👁️ Live Watch",
    "btn_watch_title": "Abrir pré-visualização de impressão sincronizada em tempo real",
    "btn_kindle": "Kindle EPUB",
    "btn_kindle_title": "Gerar EPUB para Kindle (KDP)",
    "btn_print": "Imprimir / PDF…",
    "btn_print_title": "Gerar print.html e PDF para impressão",
    "save_ready": "Pronto",
    "save_saving": "Salvando…",
    "save_saved": "Salvo ",
    "save_error": "Erro ao salvar",
    "conflict_detected": "Conflito detectado",
    "save_failed": "Falha ao salvar",
    "save_failed_msg": "Não foi possível salvar o capítulo atual antes de continuar.\nSeu texto foi preservado.",
    "save_failed_change_ch": "Não foi possível salvar o capítulo atual. Seu texto foi preservado.\nCorrija o erro antes de mudar de capítulo.",
    "btn_retry_save": "Tentar Salvar Novamente",
    "btn_retry": "Tentar novamente",
    "covers_header": "Capas",
    "btn_add_cover": "＋ Adicionar…",
    "untitled": "Sem título",
    "empty_title": "Começar um novo livro",
    "empty_desc": "Você pode criar um novo projeto do zero ou abrir uma pasta existente (cofre Markdown).",
    "btn_create_new_empty": "✨ Criar novo livro…",
    "btn_open_folder_empty": "📁 Abrir pasta existente…",
    "words_count": "{count} palavras",
    "words_short": "palavras",
    "vw_editor": "Editor",
    "vw_split": "Editor + Prévia",
    "vw_preview": "Pré-visualização",
    "editor_placeholder": "Escreva aqui… salvamento automático ativo.",
    "open_modal_title": "Abrir ou criar livro",
    "open_choice_new": "✨ Criar um novo livro do zero",
    "open_choice_folder": "📁 Pasta de livro existente (Markdown vault)",
    "open_choice_archive": "📕 Importar arquivo de projeto → converter para Markdown",
    "open_choice_docx": "📄 Importar manuscrito (.docx, .vellum, .md) → converter em livro",
    "btn_cancel": "Cancelar",
    "np_modal_title": "✨ Criar Novo Projeto de Livro",
    "np_desc": "Uma pasta dedicada (Markdown vault) será criada pronta para escrita, com book.yaml, capítulo inicial e pastas de capas e imagens.",
    "np_book_title_label": "Título do livro *",
    "np_book_title_ph": "ex. O Segredo do Rio",
    "np_author_label": "Nome do autor",
    "np_author_ph": "ex. João Silva",
    "np_parent_dir_label": "Pasta pai (onde salvar o projeto)",
    "np_parent_dir_ph": "Selecionar pasta...",
    "btn_browse": "Procurar…",
    "np_folder_name_label": "Nome da pasta",
    "np_folder_name_ph": "ex. o-segredo-do-rio",
    "np_initial_style_label": "Estilo editorial inicial",
    "btn_create_project": "Criar Projeto",
    "fs_modal_title": "Escolher…",
    "fs_home": "🏠 Início",
    "fs_volumes": "📂 Volumes",
    "fs_up": "↑ Cima",
    "fs_new_folder": "Nova pasta…",
    "fs_select": "Escolher",
    "fs_select_file": "Escolher arquivo",
    "fs_select_folder": "Escolher pasta",
    "fs_select_files_count": "Escolher {n} arquivos",
    "fs_open_folder": "📁 Abrir esta pasta",
    "fs_new_folder_prompt": "Nome da nova pasta:",
    "btn_report_issue": "Relatar um problema",
    "btn_report_issue_title": "Relatar um problema ou ver diagnósticos",
    "report_modal_title": "🛠️ Relatar um problema",
    "report_privacy_notice": "Um relatório de diagnóstico local é preparado com versão, ambiente e erros registrados. Nenhum texto de manuscrito, capítulo, PDF ou dado pessoal é anexado ou enviado automaticamente.",
    "report_desc_label": "Descreva o que você estava fazendo ou o que não funciona *",
    "report_desc_ph": "Ex.: Tentei exportar o PDF para impressão, mas a operação falhou...",
    "report_expected_label": "Resultado esperado (opcional)",
    "report_observed_label": "Resultado observado (opcional)",
    "report_recent_errors_label": "Erros recentes registrados pelo sistema:",
    "report_no_recent_errors": "Nenhum erro recente registrado.",
    "report_preview_title": "Pré-visualização do relatório gerado:",
    "report_btn_prepare": "Preparar relatório",
    "report_btn_copy": "📋 Copiar relatório",
    "report_btn_export": "💾 Exportar para arquivo…",
    "report_copied_msg": "Copiado para a área de transferência!",
    "empty_folder": "Pasta vazia",
    "print_modal_title": "PDF / Impressão — Parâmetros",
    "print_modal_desc": "Estilo do livro (salvo no vault, aplica-se à impressão, PDF e pré-visualização):",
    "style_label": "Estilo",
    "body_font_label": "Fonte do texto",
    "scene_break_label": "Quebras de cena",
    "opening_label": "Início de capítulo",
    "opening_dropcap": "Capitular",
    "opening_smallcaps": "Versaletes (Adelphi/Penguin)",
    "opening_none": "Simples alinhado",
    "trim_label": "Formato de corte",
    "pages_label": "Páginas estimadas",
    "pages_est_ph": "≈ {pages} ({words} palavras)",
    "bleed_label": "sangria total",
    "preview_chapter_label": "Capítulo de prévia:",
    "open_full_viewprint": "📖 Ver livro completo diagramado",
    "dest_folder_lbl_html": "Destino: <b>out-stampa/</b> (na pasta do livro)",
    "dest_folder_custom": "Destino: {dir}",
    "btn_pick_other_dir": "Escolher outra pasta…",
    "kdp_proof_note": "A prova final continua sendo o KDP Print Previewer.",
    "btn_start_build": "Iniciar compilação",
    "preview_no_text": "Ainda não há texto neste capítulo.",
    "preview_empty": "Este capítulo está vazio.",
    "no_chapters_yet": "Sem capítulos",
    "new_chapter_title": "Adicionar ao livro",
    "nc_type_label": "Tipo de seção",
    "nc_type_chapter": "📖 Capítulo padrão",
    "nc_type_dedication": "📜 Dedicatória (Front matter)",
    "nc_type_epigraph": "✒️ Epígrafe (Front matter)",
    "nc_type_preface": "📑 Prefácio / Introdução (Front matter)",
    "nc_type_acknowledgments": "🙏 Agradecimentos (Back matter)",
    "nc_type_about": "👤 Sobre o Autor (Back matter)",
    "nc_title_label": "Título",
    "nc_title_ph": "ex. Capítulo 1",
    "nc_position_label": "Posição",
    "nc_pos_after": "Logo após o capítulo selecionado",
    "nc_pos_end": "No final do livro",
    "nc_pos_start": "No início do livro",
    "btn_add": "Adicionar",
    "default_dedication": "Dedicatória",
    "default_epigraph": "Epígrafe",
    "default_about_author": "Sobre o Autor",
    "default_acknowledgments": "Agradecimentos",
    "default_preface": "Prefácio",
    "modal_close": "Fechar",
    "modal_error": "Erro",
    "build_running_title": "Compilação em andamento…",
    "build_running_desc": "Aguarde, compilando com pubcraft…",
    "build_pdf_success": "PDF criado com sucesso ✔",
    "build_success": "Concluído ✔",
    "btn_open_pdf": "Abrir PDF 📄",
    "btn_print_browser": "Imprimir / Salvar como PDF agora (Navegador) 🖨",
    "btn_show_in_finder": "Mostrar no Finder",
    "btn_show_in_folder": "Mostrar na pasta",
    "unsaved_changes_title": "Alterações não salvas",
    "no_book_found_title": "Nenhum livro encontrado",
    "no_book_found_desc": "A pasta selecionada não contém arquivos Markdown (.md) nem book.yaml.\n\nUse 'Novo livro' para criar um novo projeto.",
    "confirm_delete_chapter": "Remover \"{title}\" da tabela de capítulos?\n(O arquivo no disco NÃO será excluído)",
    "cover_set_as": "Definir como capa",
    "cover_delete": "Excluir esta imagem da pasta de capas?",
    "importing_doc": "Importando manuscrito…",
    "importing_wait": "Convertendo {dir}\nAguarde…",
    "importing_book": "Importando livro…",
    "importing_book_wait": "Descompactando {dir}\nAguarde…",
    "import_failed": "Falha na importação",
    "sb_asterisks": "Asteriscos (* * *)",
    "sb_fleuron": "Florão (arabesco)",
    "sb_lozenge": "Losango",
    "sb_line": "Linha com losango",
    "order_saved": "Ordem salva",
    "loading_chapter": "Carregando capítulo…",
    "opened_time": "Aberto às {time}",
    "load_error": "Erro de carregamento",
    "editing": "Editando…",
    "publish_ok": "Publicado com sucesso",
    "build_error": "Erro de compilação",
    "created_time": "Criado às {time}",
    "pick_docx_title": "Escolha o manuscrito (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "Escolha o arquivo para converter em Markdown",
    "pick_vault_title": "Escolha a pasta do livro (vault)",
    "pick_print_dir_title": "Pasta onde salvar print.html e o PDF",
    "pick_cover_title": "Escolha o arquivo de capa para copiar para cover/",
    "pick_np_dir_title": "Escolha onde salvar o projeto do livro",
    "enter_book_title": "Por favor, insira um título para o livro.",
    "select_project_folder": "Por favor, selecione uma pasta para salvar o projeto.",
    "enter_section_title": "Por favor, insira um título para a seção.",
    "move_to_trash_confirm": "Mover \"{name}\" para a lixeira (.cestina)?",
    "use_as_cover_confirm": "Usar \"{name}\" como capa do EPUB?\n\n(Será copiado para images/ e configurado no book.yaml)",
    "cover_epub_img_note": "Para a capa do EPUB é necessária uma imagem (JPEG/PNG). O KDP recomenda JPEG/TIFF com lado maior de 2560px.",
    "project_creating_title": "Criando projeto…",
    "project_creating_wait": "Criando pasta do livro e book.yaml…",
    "btn_pub_preview": "📖 Pré-visualização…",
    "btn_pub_preview_title": "Abrir pré-visualização de publicação (EPUB e PDF real)",
    "pub_preview_title": "Pré-visualização de Publicação",
    "pub_target_print": "PDF impressão",
    "pub_target_ebook": "Ebook",
    "btn_pub_update": "Atualizar pré-visualização",
    "pub_status_none": "Sem pré-visualização",
    "pub_status_generating": "A gerar…",
    "pub_status_current": "Atualizado",
    "pub_status_stale": "Desatualizado",
    "pub_status_error": "Erro",
    "pub_download_file": "📥 Descarregar ficheiro",
    "pub_bleed_label": "Sangria",
    "pub_view_label": "Visualização:",
    "pub_view_single": "Página única",
    "pub_view_facing": "Páginas lado a lado",
    "pub_guides_label": "Guias:",
    "pub_guide_cut": "Corte",
    "pub_guide_safe": "Área segura",
    "pub_profile_label": "Perfil:",
    "pub_profile_reader": "E-reader",
    "pub_profile_tablet": "Tablet",
    "pub_profile_phone": "Smartphone",
    "pub_orient_label": "Orientação:",
    "pub_orient_portrait": "Vertical",
    "pub_orient_landscape": "Horizontal",
    "pub_text_size_label": "Texto:",
    "pub_font_reset": "Repor",
    "pub_kindle_disclaimer": "A apresentação pode variar conforme o leitor. Para Kindle, confirme também no Kindle Previewer.",
    "pub_nav_jump_label": "Ir para:",
    "pub_nav_prev": "◀ Anterior",
    "pub_nav_next": "Seguinte ▶",
    "pub_zoom_label": "Zoom:",
    "pub_zoom_fit_width": "Ajustar à largura",
    "pub_zoom_fit_page": "Ajustar à página",
    "pub_empty_title": "Nenhuma pré-visualização gerada",
    "pub_prompt_update": "Clique em 'Atualizar pré-visualização' para compilar e visualizar o documento real.",
    "pub_loading_desc": "A compilar a paginação real e a renderizar os bytes gerados.",
    "pub_error_title": "Erro ao gerar pré-visualização",
    "pub_page_indicator": "Página {p} de {total}",
    "pub_recto": "Frente (Direita)",
    "pub_verso": "Verso (Esquerda)",
    "tb_bold": "Negrito (Cmd+B)",
    "tb_italic": "Itálico (Cmd+I)",
    "tb_h2": "Subtítulo (H2)",
    "tb_h3": "Seção (H3)",
    "tb_quote": "Citação (>)",
    "tb_ul": "Lista com marcadores (-)",
    "tb_ol": "Lista numerada (1.)",
    "tb_break": "Quebra de cena (***)",
    "tb_link": "Inserir link (Cmd+K)",
    "tb_image": "Inserir imagem",
    "tb_table": "Inserir tabela",
    "table_modal_title": "Inserir Tabela",
    "table_cols": "Colunas:",
    "table_rows": "Linhas de dados:",
    "table_insert": "Inserir",
    "table_col_prefix": "Coluna",
    "table_cell_prefix": "Célula",
    "welcome_version": "Versão 1.0.0-alpha.1",
    "welcome_tutorial_link": "Mostrar tutorial do PubCraft",
    "welcome_shortcuts_link": "Ajuda e atalhos de teclado",
    "welcome_btn_new": "✨ Novo Livro…",
    "welcome_btn_docx": "📄 Importar manuscrito (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 Abrir pasta existente…",
    "recents_header": "Arquivos recentes",
    "recents_search_placeholder": "Filtrar projetos…",
    "recents_workspace_label": "Área de trabalho:",
    "recents_change_workspace": "Alterar…",
    "recents_all_tab": "Todos os recentes",
    "recents_workspace_tab": "Na área de trabalho",
    "recents_empty_title": "Nenhum livro recente",
    "recents_empty_desc": "Crie um novo livro ou abra uma pasta para começar.",
    "recents_btn_open_other": "Abrir outro…",
    "recents_btn_open_selected": "Abrir selecionado",
    "recents_remove_tooltip": "Remover dos recentes (não exclui arquivos do disco)",
    "topbar_recents": "📚 Projetos recentes",
    "topbar_recents_title": "Abrir projetos recentes e inicial",
    "topbar_close_book": "✕ Fechar livro",
    "topbar_close_book_title": "Fechar livro atual",
    "fs_workspace": "⭐ Área de trabalho",
    "fs_documents": "📄 Documentos",
    "fs_desktop": "🖥️ Mesa",
    "fs_downloads": "📥 Downloads",
    "fs_filter_placeholder": "Filtrar pastas…",
    "fs_set_workspace_btn": "📌 Definir como área de trabalho padrão",
    "fs_workspace_saved": "Área de trabalho atualizada",
    "tutorial_modal_title": "Guia e Tutorial do PubCraft",
    "tut_tab_guide": "Tutorial",
    "tut_tab_shortcuts": "Atalhos",
    "topbar_title_tooltip": "Clique para renomear o livro",
    "notebook_header": "Caderno",
    "notebook_section_tooltip": "Notas e ideias para este livro",
    "btn_add_note": "＋ Nova nota…",
    "btn_toggle_notebook": "📓 Notas",
    "btn_focus_mode": "Foco",
    "btn_focus_mode_title": "Modo sem distrações / Tela cheia (F11 ou Esc para sair)",
    "focus_exit": "Sair",
    "focus_font_typewriter": "⌨️ Máquina de escrever",
    "focus_font_serif": "📖 Fonte Serif",
    "theme_dark": "Escuro",
    "theme_light": "Claro",
    "theme_toggle_title": "Alternar modo Escuro / Claro",
    "notebook_drawer_title": "Caderno do livro",
    "btn_new_note": "Nova nota",
    "note_title_ph": "Título da nota…",
    "note_content_ph": "Ideias, pesquisas, lembretes de enredo, cenas para desenvolver…",
    "note_insert_title": "Inserir o texto da nota no capítulo atual no cursor",
    "note_insert": "Inserir no manuscrito",
    "note_convert_title": "Transformar esta nota em um capítulo oficial do livro",
    "note_convert": "Converter em capítulo",
    "note_empty": "Nenhuma nota ainda",
    "note_untitled": "Nota sem título",
    "note_unsaved": "Não salvo",
    "note_loading": "Carregando…",
    "note_del_title": "Excluir nota",
    "note_del_confirm": "Excluir a nota \"{title}\" do caderno?",
    "note_convert_confirm": "Converter esta nota em um capítulo oficial do livro? Ela será adicionada ao índice e ao book.yaml.",
    "note_inserted": "Inserido no capítulo!",
    "btn_delete": "Excluir"
  },
  "zh": {
    "chapter": "第",
    "cancel": "取消",
    "loading": "加载中…",

    "focus_exit_title": "退出免打扰模式 (Esc)",
    "focus_toggle_font_title": "切换字体：打字机 / 衬线体",
    "focus_dim_off": "👁️ 专注：关闭",
    "focus_dim_sentence": "🎯 专注：单句",
    "focus_dim_paragraph": "📑 专注：段落",
    "focus_dim_title": "切换专注模式 (关闭 / 单句 / 段落，类似 iA Writer)",
    "focus_super_focus_title": "切换超级专注 (垂直居中滚动打字机模式)",
    "focus_toggle_super_focus_title": "切换超级专注模式 (居中连续滚动)",
    "focus_super_focus_on": "🎯 超级专注：开启",
    "focus_super_focus_off": "🎯 超级专注：关闭",
    "focus_select_chapter": "选择章节",
    "focus_select_chapter_title": "选择章节",
    "recents_prune_missing": "清理丢失项目",
    "recents_prune_title": "从最近列表中清除磁盘上已不存在的项目文件夹",
    "recents_btn_clear_all": "清空列表",
    "recents_clear_confirm_title": "清空最近打开的项目",
    "recents_clear_confirm_desc": "是否从列表中移除所有最近项目？（磁盘上的原始文件不会被删除）。",
    "recent_missing_badge": "未找到",
    "recent_missing_title": "未找到图书",
    "recent_missing_desc": "此图书所在的磁盘文件夹已不存在：",
    "recent_missing_prompt": "是否将其从最近项目中移除？",
    "recent_remove_btn": "从列表中移除",

    "btn_new_project": "✨ 新建图书…",
    "btn_new_project_title": "创建新图书项目 (生成文件夹与 book.yaml)",
    "btn_open": "打开…",
    "btn_open_title": "打开已有图书文件夹或导入文件",
    "btn_new_chapter": "＋ 新建章节",
    "btn_new_chapter_title": "在当前图书中创建新章节",
    "btn_style": "风格：",
    "btn_style_title": "图书排版风格：适用于印刷、PDF 和预览",
    "btn_opening": "段首：",
    "btn_opening_title": "章节段首样式：首字下沉、小型大写或顶格",
    "btn_watch": "👁️ 实时预览",
    "btn_watch_title": "打开实时同步的印刷排版预览",
    "btn_kindle": "Kindle EPUB",
    "btn_kindle_title": "生成适用于 Kindle (KDP) 的 EPUB 电子书",
    "btn_print": "打印 / PDF…",
    "btn_print_title": "生成用于出版印刷的 print.html 和 PDF",
    "save_ready": "就绪",
    "save_saving": "正在保存…",
    "save_saved": "已保存 ",
    "save_error": "保存错误",
    "conflict_detected": "检测到冲突",
    "save_failed": "保存失败",
    "save_failed_msg": "继续操作前无法保存当前章节。\n您输入的内容已完整保留。",
    "save_failed_change_ch": "无法保存当前章节。您输入的内容已完整保留。\n请在更换章节前修复错误或重试。",
    "btn_retry_save": "重试保存",
    "btn_retry": "重试",
    "covers_header": "封面",
    "btn_add_cover": "＋ 添加…",
    "untitled": "无标题",
    "empty_title": "开始一本新书",
    "empty_desc": "您可以从零创建新图书项目，或打开已有的 Markdown 文件夹。",
    "btn_create_new_empty": "✨ 创建新图书…",
    "btn_open_folder_empty": "📁 打开已有文件夹…",
    "words_count": "{count} 字",
    "words_short": "词",
    "vw_editor": "编辑器",
    "vw_split": "分屏预览",
    "vw_preview": "预览",
    "editor_placeholder": "在此输入… 自动保存已启用。",
    "open_modal_title": "打开或创建图书",
    "open_choice_new": "✨ 从零创建一本新书",
    "open_choice_folder": "📁 已有图书文件夹 (Markdown vault)",
    "open_choice_archive": "📕 导入项目归档 → 转换为 Markdown",
    "open_choice_docx": "📄 导入手稿 (.docx, .vellum, .md) → 转换为书籍",
    "btn_cancel": "取消",
    "np_modal_title": "✨ 创建新图书项目",
    "np_desc": "系统将创建一个专用的 Markdown 文件夹，配置好 book.yaml、初始章节以及封面与图片目录。",
    "np_book_title_label": "书名 *",
    "np_book_title_ph": "例如：河流的秘密",
    "np_author_label": "作者姓名",
    "np_author_ph": "例如：张三",
    "np_parent_dir_label": "上级文件夹 (项目保存位置)",
    "np_parent_dir_ph": "选择文件夹...",
    "btn_browse": "浏览…",
    "np_folder_name_label": "文件夹名称",
    "np_folder_name_ph": "例如：the-rivers-secret",
    "np_initial_style_label": "初始编辑排版风格",
    "btn_create_project": "创建项目",
    "fs_modal_title": "选择…",
    "fs_home": "🏠 主目录",
    "fs_volumes": "📂 磁盘卷",
    "fs_up": "↑ 向上",
    "fs_new_folder": "新建文件夹…",
    "fs_select": "选择",
    "fs_select_file": "选择文件",
    "fs_select_folder": "选择文件夹",
    "fs_select_files_count": "选择 {n} 个文件",
    "fs_open_folder": "📁 打开此文件夹",
    "fs_new_folder_prompt": "新建文件夹名称：",
    "btn_report_issue": "报告问题",
    "btn_report_issue_title": "报告问题或查看诊断信息",
    "report_modal_title": "🛠️ 报告问题",
    "report_privacy_notice": "将生成包含版本、环境和记录错误的本地诊断报告。不会自动附加或发送任何书稿文本、章节内容、PDF 或个人数据。",
    "report_desc_label": "请描述您的操作或出现的异常 *",
    "report_desc_ph": "例如：尝试导出印刷 PDF 时操作失败...",
    "report_expected_label": "预期结果（可选）",
    "report_observed_label": "实际现象（可选）",
    "report_recent_errors_label": "系统最近记录的错误：",
    "report_no_recent_errors": "未记录到近期错误。",
    "report_preview_title": "生成报告预览：",
    "report_btn_prepare": "生成报告",
    "report_btn_copy": "📋 复制报告",
    "report_btn_export": "💾 导出到文件…",
    "report_copied_msg": "已复制到剪贴板！",
    "empty_folder": "空文件夹",
    "print_modal_title": "PDF / 打印 — 参数设置",
    "print_modal_desc": "图书风格 (保存在 vault 中，适用于打印、PDF 与预览)：",
    "style_label": "排版风格",
    "body_font_label": "正文字体",
    "scene_break_label": "场景分隔符",
    "opening_label": "章节段首",
    "opening_dropcap": "首字下沉",
    "opening_smallcaps": "小型大写 (Adelphi/Penguin)",
    "opening_none": "普通顶格",
    "trim_label": "开本尺寸",
    "pages_label": "预估页数",
    "pages_est_ph": "≈ {pages} ({words} 字)",
    "bleed_label": "全出血 (Bleed)",
    "preview_chapter_label": "预览章节：",
    "open_full_viewprint": "📖 查看排版完整的全书",
    "dest_folder_lbl_html": "保存目标：<b>out-stampa/</b> (图书项目文件夹内)",
    "dest_folder_custom": "保存目标：{dir}",
    "btn_pick_other_dir": "选择其他文件夹…",
    "kdp_proof_note": "最终效果请以 KDP Print Previewer 为准。",
    "btn_start_build": "开始生成",
    "preview_no_text": "该章节尚无内容。",
    "preview_empty": "本章暂无内容。",
    "no_chapters_yet": "暂无章节",
    "new_chapter_title": "添加到图书",
    "nc_type_label": "章节类型",
    "nc_type_chapter": "📖 标准章节",
    "nc_type_dedication": "📜 题献 (前言部分)",
    "nc_type_epigraph": "✒️ 题词 (前言部分)",
    "nc_type_preface": "📑 序言 / 导言 (前言部分)",
    "nc_type_acknowledgments": "🙏 致谢 (后记部分)",
    "nc_type_about": "👤 关于作者 (后记部分)",
    "nc_title_label": "标题",
    "nc_title_ph": "例如：第一章",
    "nc_position_label": "位置",
    "nc_pos_after": "紧接着当前选中的章节",
    "nc_pos_end": "在图书末尾",
    "nc_pos_start": "在图书开头",
    "btn_add": "添加",
    "default_dedication": "题献",
    "default_epigraph": "题词",
    "default_about_author": "关于作者",
    "default_acknowledgments": "致谢",
    "default_preface": "序言",
    "modal_close": "关闭",
    "modal_error": "错误",
    "build_running_title": "正在生成…",
    "build_running_desc": "请稍候，pubcraft 正在编译图书…",
    "build_pdf_success": "PDF 成功生成 ✔",
    "build_success": "完成 ✔",
    "btn_open_pdf": "打开 PDF 📄",
    "btn_print_browser": "立即打印 / 保存为 PDF (浏览器) 🖨",
    "btn_show_in_finder": "在访达中显示",
    "btn_show_in_folder": "在文件夹中显示",
    "unsaved_changes_title": "未保存的更改",
    "no_book_found_title": "未找到图书",
    "no_book_found_desc": "所选文件夹不包含 Markdown 文件 (.md) 或 book.yaml 文件。\n\n请使用“新建图书”创建新项目。",
    "confirm_delete_chapter": "确定从目录中移除 \"{title}\" 吗？\n(磁盘上的源文件不会被删除)",
    "cover_set_as": "设为图书封面",
    "cover_delete": "从封面文件夹中删除此图片？",
    "importing_doc": "正在导入手稿…",
    "importing_wait": "正在转换 {dir}\n请稍候…",
    "importing_book": "正在导入图书…",
    "importing_book_wait": "正在解压 {dir}\n请稍候…",
    "import_failed": "导入失败",
    "sb_asterisks": "星号 (* * *)",
    "sb_fleuron": "花符 (Fleuron)",
    "sb_lozenge": "菱形",
    "sb_line": "带菱形分割线",
    "order_saved": "顺序已保存",
    "loading_chapter": "正在加载章节…",
    "opened_time": "打开于 {time}",
    "load_error": "加载错误",
    "editing": "正在编辑…",
    "publish_ok": "发布成功",
    "build_error": "编译错误",
    "created_time": "创建于 {time}",
    "pick_docx_title": "选择手稿 (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "选择要转换为 Markdown 的压缩包",
    "pick_vault_title": "选择图书文件夹 (vault)",
    "pick_print_dir_title": "选择保存 print.html 和 PDF 的文件夹",
    "pick_cover_title": "选择要复制到 cover/ 目录的封面图片",
    "pick_np_dir_title": "选择保存图书项目的位置",
    "enter_book_title": "请输入书名。",
    "select_project_folder": "请选择用于保存项目的文件夹。",
    "enter_section_title": "请输入章节标题。",
    "move_to_trash_confirm": "确定将 \"{name}\" 移动到回收站 (.cestina) 吗？",
    "use_as_cover_confirm": "确定将 \"{name}\" 设为 EPUB 封面吗？\n\n(将复制至 images/ 并在 book.yaml 中配置)",
    "cover_epub_img_note": "EPUB 封面需使用图像文件 (JPEG/PNG)。KDP 推荐长边为 2560px 的 JPEG/TIFF。",
    "project_creating_title": "正在创建项目…",
    "project_creating_wait": "正在创建图书文件夹及 book.yaml…",
    "btn_pub_preview": "📖 出版预览…",
    "btn_pub_preview_title": "打开出版预览（真实 EPUB 与印刷 PDF）",
    "pub_preview_title": "出版预览",
    "pub_target_print": "印刷 PDF",
    "pub_target_ebook": "电子书",
    "btn_pub_update": "更新预览",
    "pub_status_none": "暂无预览",
    "pub_status_generating": "正在生成…",
    "pub_status_current": "已更新",
    "pub_status_stale": "待更新",
    "pub_status_error": "错误",
    "pub_download_file": "📥 下载文件",
    "pub_bleed_label": "出血",
    "pub_view_label": "视图：",
    "pub_view_single": "单页",
    "pub_view_facing": "双页对开",
    "pub_guides_label": "参考线：",
    "pub_guide_cut": "裁切线",
    "pub_guide_safe": "安全区",
    "pub_profile_label": "设备：",
    "pub_profile_reader": "电子阅读器",
    "pub_profile_tablet": "平板电脑",
    "pub_profile_phone": "智能手机",
    "pub_orient_label": "方向：",
    "pub_orient_portrait": "竖向",
    "pub_orient_landscape": "横向",
    "pub_text_size_label": "字号：",
    "pub_font_reset": "重置",
    "pub_kindle_disclaimer": "显示效果因阅读软件而异。Kindle 请通过 Kindle Previewer 复核。",
    "pub_nav_jump_label": "跳转至：",
    "pub_nav_prev": "◀ 上一页",
    "pub_nav_next": "下一页 ▶",
    "pub_zoom_label": "缩放：",
    "pub_zoom_fit_width": "适应宽度",
    "pub_zoom_fit_page": "适应页面",
    "pub_empty_title": "尚未生成预览",
    "pub_prompt_update": "点击“更新预览”以编译并查看真实排版。",
    "pub_loading_desc": "正在计算真实版面并渲染生成的文件字节。",
    "pub_error_title": "生成预览失败",
    "pub_page_indicator": "第 {p} 页，共 {total} 页",
    "pub_recto": "右页（正面）",
    "pub_verso": "左页（背面）",
    "tb_bold": "加粗 (Cmd+B)",
    "tb_italic": "斜体 (Cmd+I)",
    "tb_h2": "二级标题 (H2)",
    "tb_h3": "三级标题 (H3)",
    "tb_quote": "引用 (>)",
    "tb_ul": "无序列表 (-)",
    "tb_ol": "有序列表 (1.)",
    "tb_break": "场景分隔 (***)",
    "tb_link": "插入链接 (Cmd+K)",
    "tb_image": "插入图片",
    "tb_table": "插入表格",
    "table_modal_title": "插入表格",
    "table_cols": "列数：",
    "table_rows": "数据行数：",
    "table_insert": "插入",
    "table_col_prefix": "列",
    "table_cell_prefix": "单元格",
    "welcome_version": "版本 1.0.0-alpha.1",
    "welcome_tutorial_link": "显示 PubCraft 教程",
    "welcome_shortcuts_link": "帮助与键盘快捷键",
    "welcome_btn_new": "✨ 新建图书…",
    "welcome_btn_docx": "📄 导入手稿 (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 打开已有文件夹…",
    "recents_header": "最近文件",
    "recents_search_placeholder": "过滤项目…",
    "recents_workspace_label": "工作区：",
    "recents_change_workspace": "更改…",
    "recents_all_tab": "所有最近",
    "recents_workspace_tab": "在工作区中",
    "recents_empty_title": "暂无最近图书",
    "recents_empty_desc": "新建图书或打开已有文件夹即可开始编写。",
    "recents_btn_open_other": "打开其他…",
    "recents_btn_open_selected": "打开选中项",
    "recents_remove_tooltip": "从最近列表中移除（不会删除磁盘文件）",
    "topbar_recents": "📚 最近项目",
    "topbar_recents_title": "打开最近项目与启动台",
    "topbar_close_book": "✕ 关闭图书",
    "topbar_close_book_title": "关闭当前图书",
    "fs_workspace": "⭐ 工作区",
    "fs_documents": "📄 文档",
    "fs_desktop": "🖥️ 桌面",
    "fs_downloads": "📥 下载",
    "fs_filter_placeholder": "过滤文件夹…",
    "fs_set_workspace_btn": "📌 设为默认工作区",
    "fs_workspace_saved": "工作区已更新",
    "tutorial_modal_title": "PubCraft 指南与教程",
    "tut_tab_guide": "教程",
    "tut_tab_shortcuts": "快捷键",
    "topbar_title_tooltip": "点击重命名书籍",
    "notebook_header": "笔记本",
    "notebook_section_tooltip": "本书的笔记与灵感",
    "btn_add_note": "＋ 新建笔记…",
    "btn_toggle_notebook": "📓 笔记",
    "btn_focus_mode": "专注模式",
    "btn_focus_mode_title": "全屏免打扰专注模式 (按 F11 或 Esc 退出)",
    "focus_exit": "退出",
    "focus_font_typewriter": "⌨️ 打字机字体",
    "focus_font_serif": "📖 衬线字体",
    "theme_dark": "深色",
    "theme_light": "浅色",
    "theme_toggle_title": "切换深色 / 浅色模式",
    "notebook_drawer_title": "书籍笔记本",
    "btn_new_note": "新建笔记",
    "note_title_ph": "笔记标题…",
    "note_content_ph": "灵感、调研、情节备忘、待展开场景…",
    "note_insert_title": "在当前章节光标处插入笔记内容",
    "note_insert": "插入手稿",
    "note_convert_title": "将此笔记转换为正式章节",
    "note_convert": "转换为章节",
    "note_empty": "暂无笔记",
    "note_untitled": "无标题笔记",
    "note_unsaved": "未保存",
    "note_loading": "正在加载…",
    "note_del_title": "删除笔记",
    "note_del_confirm": "确定从笔记本中删除 \"{title}\"？",
    "note_convert_confirm": "是否将此笔记转换为正式章节？将添加到目录和 book.yaml。",
    "note_inserted": "已插入章节！",
    "btn_delete": "删除"
  },
  "ja": {
    "chapter": "第",
    "cancel": "キャンセル",
    "loading": "読み込み中…",

    "focus_exit_title": "集中モードを終了 (Esc)",
    "focus_toggle_font_title": "フォント切替: タイプライター / 明朝体",
    "focus_dim_off": "👁️ 集中: オフ",
    "focus_dim_sentence": "🎯 集中: 文単位",
    "focus_dim_paragraph": "📑 集中: 段落単位",
    "focus_dim_title": "集中モード切替 (オフ / 文 / 段落 iA Writer風)",
    "focus_super_focus_title": "スーパー集中モード切替 (中央スクロール表示)",
    "focus_toggle_super_focus_title": "スーパー集中モード切替 (中央連続スクロール)",
    "focus_super_focus_on": "🎯 スーパー集中: オン",
    "focus_super_focus_off": "🎯 スーパー集中: オフ",
    "focus_select_chapter": "章を選択",
    "focus_select_chapter_title": "章を選択",
    "recents_prune_missing": "見つからない項目を削除",
    "recents_prune_title": "ディスク上に存在しないプロジェクトフォルダを一覧から削除します",
    "recents_btn_clear_all": "すべてクリア",
    "recents_clear_confirm_title": "最近の項目を消去",
    "recents_clear_confirm_desc": "最近使用したすべてのプロジェクトを一覧から削除しますか？（ディスク上の実ファイルは削除されません）。",
    "recent_missing_badge": "見つかりません",
    "recent_missing_title": "書籍が見つかりません",
    "recent_missing_desc": "この書籍のフォルダはディスク上に存在しません:",
    "recent_missing_prompt": "最近のプロジェクトから削除しますか？",
    "recent_remove_btn": "一覧から削除",

    "btn_new_project": "✨ 新規書籍…",
    "btn_new_project_title": "新しい書籍プロジェクトを作成 (フォルダと book.yaml)",
    "btn_open": "開く…",
    "btn_open_title": "既存の書籍フォルダを開く、またはファイルをインポート",
    "btn_new_chapter": "＋ 新規章",
    "btn_new_chapter_title": "開いている書籍に新しい章を追加",
    "btn_style": "スタイル：",
    "btn_style_title": "書籍スタイル：印刷、PDF、プレビューに適用",
    "btn_opening": "章頭：",
    "btn_opening_title": "章の開始スタイル：ドロップキャップ、スモールキャップス、または標準",
    "btn_watch": "👁️ ライブ監視",
    "btn_watch_title": "リアルタイム同期された印刷プレビューを開く",
    "btn_kindle": "Kindle EPUB",
    "btn_kindle_title": "Kindle (KDP) 用の EPUB を生成",
    "btn_print": "印刷 / PDF…",
    "btn_print_title": "印刷用の print.html と PDF を生成",
    "save_ready": "準備完了",
    "save_saving": "保存中…",
    "save_saved": "保存済み ",
    "save_error": "保存エラー",
    "conflict_detected": "競合が検出されました",
    "save_failed": "保存に失敗しました",
    "save_failed_msg": "続行する前に現在の章を保存できませんでした。\n入力されたテキストは保護されています。",
    "save_failed_change_ch": "現在の章を保存できませんでした。入力内容は保護されています。\n章を切り替える前にエラーを解消するか再試行してください。",
    "btn_retry_save": "保存を再試行",
    "btn_retry": "再試行",
    "covers_header": "表紙",
    "btn_add_cover": "＋ 追加…",
    "untitled": "無題",
    "empty_title": "新しい本を始める",
    "empty_desc": "ゼロからプロジェクトを作成するか、既存の Markdown フォルダを開くことができます。",
    "btn_create_new_empty": "✨ 新規書籍を作成…",
    "btn_open_folder_empty": "📁 既存のフォルダを開く…",
    "words_count": "{count} 語",
    "words_short": "語",
    "vw_editor": "エディタ",
    "vw_split": "分割表示",
    "vw_preview": "プレビュー",
    "editor_placeholder": "ここに入力… 自動保存されます。",
    "open_modal_title": "本を開く / 作成",
    "open_choice_new": "✨ 新規書籍をゼロから作成",
    "open_choice_folder": "📁 既存の書籍フォルダ (Markdown vault)",
    "open_choice_archive": "📕 プロジェクトアーカイブのインポート → Markdownへ変換",
    "open_choice_docx": "📄 原稿 (.docx, .vellum, .md) → 書籍に変換",
    "btn_cancel": "キャンセル",
    "np_modal_title": "✨ 新規書籍プロジェクトの作成",
    "np_desc": "book.yaml、初期の章、表紙および画像フォルダを備えた執筆専用フォルダ (Markdown vault) が作成されます。",
    "np_book_title_label": "書籍タイトル *",
    "np_book_title_ph": "例: 川の秘密",
    "np_author_label": "著者名",
    "np_author_ph": "例: 山田太郎",
    "np_parent_dir_label": "親フォルダ (保存先)",
    "np_parent_dir_ph": "フォルダを選択...",
    "btn_browse": "参照…",
    "np_folder_name_label": "フォルダ名",
    "np_folder_name_ph": "例: the-rivers-secret",
    "np_initial_style_label": "初期エディトリアルスタイル",
    "btn_create_project": "プロジェクト作成",
    "fs_modal_title": "選択…",
    "fs_home": "🏠 ホーム",
    "fs_volumes": "📂 ボリューム",
    "fs_up": "↑ 上へ",
    "fs_new_folder": "新規フォルダ…",
    "fs_select": "選択",
    "fs_select_file": "ファイルを選択",
    "fs_select_folder": "フォルダを選択",
    "fs_select_files_count": "{n} 個のファイルを選択",
    "fs_open_folder": "📁 このフォルダを開く",
    "fs_new_folder_prompt": "新規フォルダ名：",
    "btn_report_issue": "問題を報告",
    "btn_report_issue_title": "問題を報告または診断情報を表示",
    "report_modal_title": "🛠️ 問題の報告",
    "report_privacy_notice": "バージョン、動作環境、記録されたエラーを含むローカル診断レポートを生成します。原稿の本文、章のテキスト、PDF、個人情報は自動で添付・送信されません。",
    "report_desc_label": "行っていた操作または発生した問題を説明してください *",
    "report_desc_ph": "例：印刷用PDFをエクスポートしようとしたが失敗した...",
    "report_expected_label": "期待される動作（任意）",
    "report_observed_label": "実際の動作（任意）",
    "report_recent_errors_label": "最近記録されたシステムエラー：",
    "report_no_recent_errors": "最近のエラーは記録されていません。",
    "report_preview_title": "生成されたレポートのプレビュー：",
    "report_btn_prepare": "レポートを生成",
    "report_btn_copy": "📋 レポートをコピー",
    "report_btn_export": "💾 ファイルに書き出し…",
    "report_copied_msg": "クリップボードにコピーしました！",
    "empty_folder": "空のフォルダ",
    "print_modal_title": "PDF / 印刷 — パラメータ",
    "print_modal_desc": "書籍スタイル (vaultに保存され、印刷・PDF・プレビューに適用)：",
    "style_label": "スタイル",
    "body_font_label": "本文フォント",
    "scene_break_label": "シーン区切り",
    "opening_label": "章の開始",
    "opening_dropcap": "ドロップキャップ",
    "opening_smallcaps": "スモールキャップス (Adelphi/Penguin)",
    "opening_none": "プレーン（インデントなし）",
    "trim_label": "用紙サイズ",
    "pages_label": "推定ページ数",
    "pages_est_ph": "≈ {pages} ({words} 語)",
    "bleed_label": "塗り足しあり",
    "preview_chapter_label": "プレビュー章：",
    "open_full_viewprint": "📖 整形された書籍全体を表示",
    "dest_folder_lbl_html": "出力先：<b>out-stampa/</b> (書籍フォルダ内)",
    "dest_folder_custom": "出力先：{dir}",
    "btn_pick_other_dir": "別のフォルダを選択…",
    "kdp_proof_note": "最終確認はKDP Print Previewerで行ってください。",
    "btn_start_build": "ビルド開始",
    "preview_no_text": "この章にはまだテキストがありません。",
    "preview_empty": "この章は空です。",
    "no_chapters_yet": "章がありません",
    "new_chapter_title": "本に追加",
    "nc_type_label": "セクションの種類",
    "nc_type_chapter": "📖 通常の章",
    "nc_type_dedication": "📜 献辞 (前付)",
    "nc_type_epigraph": "✒️ エピグラフ (前付)",
    "nc_type_preface": "📑 まえがき / 序文 (前付)",
    "nc_type_acknowledgments": "🙏 謝辞 (後付)",
    "nc_type_about": "👤 著者について (後付)",
    "nc_title_label": "タイトル",
    "nc_title_ph": "例: 第1章",
    "nc_position_label": "配置位置",
    "nc_pos_after": "選択した章の直後",
    "nc_pos_end": "本の最後",
    "nc_pos_start": "本の最初",
    "btn_add": "追加",
    "default_dedication": "献辞",
    "default_epigraph": "エピグラフ",
    "default_about_author": "著者について",
    "default_acknowledgments": "謝辞",
    "default_preface": "まえがき",
    "modal_close": "閉じる",
    "modal_error": "エラー",
    "build_running_title": "ビルド実行中…",
    "build_running_desc": "お待ちください、pubcraftでコンパイル中…",
    "build_pdf_success": "PDFが正常に作成されました ✔",
    "build_success": "完了 ✔",
    "btn_open_pdf": "PDFを開く 📄",
    "btn_print_browser": "今すぐ印刷 / PDFとして保存 (ブラウザ) 🖨",
    "btn_show_in_finder": "Finderで表示",
    "btn_show_in_folder": "フォルダで表示",
    "unsaved_changes_title": "保存されていない変更",
    "no_book_found_title": "本が見つかりません",
    "no_book_found_desc": "選択したフォルダには Markdown ファイル (.md) または book.yaml が含まれていません。\n\n「新規書籍」を使用して作成してください。",
    "confirm_delete_chapter": "\"{title}\" を目次から削除しますか？\n(ディスク上のファイルは削除されません)",
    "cover_set_as": "表紙に設定",
    "cover_delete": "表紙フォルダからこの画像を削除しますか？",
    "importing_doc": "原稿をインポート中…",
    "importing_wait": "{dir} を変換中\nお待ちください…",
    "importing_book": "書籍をインポート中…",
    "importing_book_wait": "{dir} を展開中\nお待ちください…",
    "import_failed": "インポートに失敗しました",
    "sb_asterisks": "アスタリスク (* * *)",
    "sb_fleuron": "フルーロン（花飾り）",
    "sb_lozenge": "ひし形",
    "sb_line": "ひし形付き罫線",
    "order_saved": "順序を保存しました",
    "loading_chapter": "章を読み込み中…",
    "opened_time": "{time} に開きました",
    "load_error": "読み込みエラー",
    "editing": "編集中…",
    "publish_ok": "出力完了",
    "build_error": "ビルドエラー",
    "created_time": "{time} に作成しました",
    "pick_docx_title": "原稿を選択 (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "Markdownに変換するアーカイブを選択",
    "pick_vault_title": "書籍フォルダ (Vault) を選択",
    "pick_print_dir_title": "print.html と PDF の保存先フォルダを選択",
    "pick_cover_title": "cover/ にコピーする表紙ファイルを選択",
    "pick_np_dir_title": "書籍プロジェクトの保存先を選択",
    "enter_book_title": "書籍タイトルを入力してください。",
    "select_project_folder": "プロジェクトを保存するフォルダを選択してください。",
    "enter_section_title": "セクションのタイトルを入力してください。",
    "move_to_trash_confirm": "\"{name}\" をゴミ箱 (.cestina) に移動しますか？",
    "use_as_cover_confirm": "\"{name}\" をEPUBの表紙として使用しますか？\n\n(images/ にコピーされ、book.yaml に設定されます)",
    "cover_epub_img_note": "EPUB表紙には画像ファイル (JPEG/PNG) が必要です。KDPでは長辺2560pxのJPEG/TIFFが推奨されます。",
    "project_creating_title": "プロジェクト作成中…",
    "project_creating_wait": "書籍フォルダと book.yaml を作成中…",
    "btn_pub_preview": "📖 プレビュー…",
    "btn_pub_preview_title": "出版プレビューを開く（実EPUBおよび印刷PDF）",
    "pub_preview_title": "出版プレビュー",
    "pub_target_print": "印刷用PDF",
    "pub_target_ebook": "電子書籍",
    "btn_pub_update": "プレビュー更新",
    "pub_status_none": "プレビューなし",
    "pub_status_generating": "生成中…",
    "pub_status_current": "最新",
    "pub_status_stale": "要更新",
    "pub_status_error": "エラー",
    "pub_download_file": "📥 ファイルを保存",
    "pub_bleed_label": "塗り足し",
    "pub_view_label": "表示:",
    "pub_view_single": "単一ページ",
    "pub_view_facing": "見開き",
    "pub_guides_label": "ガイド:",
    "pub_guide_cut": "仕上がり線",
    "pub_guide_safe": "セーフエリア",
    "pub_profile_label": "端末:",
    "pub_profile_reader": "電子書籍端末",
    "pub_profile_tablet": "タブレット",
    "pub_profile_phone": "スマートフォン",
    "pub_orient_label": "向き:",
    "pub_orient_portrait": "縦向き",
    "pub_orient_landscape": "横向き",
    "pub_text_size_label": "文字:",
    "pub_font_reset": "リセット",
    "pub_kindle_disclaimer": "リーダーにより表示が異なる場合があります。KindleはKindle Previewerでも確認してください。",
    "pub_nav_jump_label": "移動:",
    "pub_nav_prev": "◀ 前へ",
    "pub_nav_next": "次へ ▶",
    "pub_zoom_label": "ズーム:",
    "pub_zoom_fit_width": "幅に合わせる",
    "pub_zoom_fit_page": "ページ全体",
    "pub_empty_title": "プレビューが生成されていません",
    "pub_prompt_update": "「プレビュー更新」をクリックして実際のファイルをレンダリングします。",
    "pub_loading_desc": "実際のレイアウトを計算し、生成されたバイト列を表示しています。",
    "pub_error_title": "プレビュー生成エラー",
    "pub_page_indicator": "{p} / {total} ページ",
    "pub_recto": "奇数ページ（右）",
    "pub_verso": "偶数ページ（左）",
    "tb_bold": "太字 (Cmd+B)",
    "tb_italic": "斜体 (Cmd+I)",
    "tb_h2": "見出し 2 (H2)",
    "tb_h3": "見出し 3 (H3)",
    "tb_quote": "引用 (>)",
    "tb_ul": "箇条書きリスト (-)",
    "tb_ol": "番号付きリスト (1.)",
    "tb_break": "シーンの区切り (***)",
    "tb_link": "リンクの挿入 (Cmd+K)",
    "tb_image": "画像の挿入",
    "tb_table": "表を挿入",
    "table_modal_title": "表を挿入",
    "table_cols": "列数:",
    "table_rows": "データ行数:",
    "table_insert": "挿入",
    "table_col_prefix": "列",
    "table_cell_prefix": "セル",
    "welcome_version": "バージョン 1.0.0-alpha.1",
    "welcome_tutorial_link": "PubCraft チュートリアルを表示",
    "welcome_shortcuts_link": "ヘルプとキーボードショートカット",
    "welcome_btn_new": "✨ 新規書籍…",
    "welcome_btn_docx": "📄 原稿をインポート (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 既存のフォルダを開く…",
    "recents_header": "最近使ったファイル",
    "recents_search_placeholder": "プロジェクトを検索…",
    "recents_workspace_label": "ワークスペース：",
    "recents_change_workspace": "変更…",
    "recents_all_tab": "すべての履歴",
    "recents_workspace_tab": "ワークスペース内",
    "recents_empty_title": "最近使った書籍はありません",
    "recents_empty_desc": "新しい書籍を作成するかフォルダを開いて執筆を始めてください。",
    "recents_btn_open_other": "別のフォルダを開く…",
    "recents_btn_open_selected": "選択項目を開く",
    "recents_remove_tooltip": "最近使った一覧から削除（ディスクのファイルは削除されません）",
    "topbar_recents": "📚 最近使ったプロジェクト",
    "topbar_recents_title": "最近使ったプロジェクトとランチャーを開く",
    "topbar_close_book": "✕ 書籍を閉じる",
    "topbar_close_book_title": "現在の書籍を閉じる",
    "fs_workspace": "⭐ ワークスペース",
    "fs_documents": "📄 書類",
    "fs_desktop": "🖥️ デスクトップ",
    "fs_downloads": "📥 ダウンロード",
    "fs_filter_placeholder": "フォルダを絞り込み…",
    "fs_set_workspace_btn": "📌 デフォルトのワークスペースに設定",
    "fs_workspace_saved": "ワークスペースを更新しました",
    "tutorial_modal_title": "PubCraft ガイド & チュートリアル",
    "tut_tab_guide": "チュートリアル",
    "tut_tab_shortcuts": "ショートカット",
    "topbar_title_tooltip": "クリックして書籍名を変更",
    "notebook_header": "ノートブック",
    "notebook_section_tooltip": "この本に関するメモやアイデア",
    "btn_add_note": "＋ 新規メモ…",
    "btn_toggle_notebook": "📓 ノート",
    "btn_focus_mode": "集中モード",
    "btn_focus_mode_title": "全画面集中ライティングモード (F11またはEscで終了)",
    "focus_exit": "終了",
    "focus_font_typewriter": "⌨️ タイプライター",
    "focus_font_serif": "📖 明朝体/Serif",
    "theme_dark": "ダーク",
    "theme_light": "ライト",
    "theme_toggle_title": "ダーク / ライトモード切り替え",
    "notebook_drawer_title": "書籍ノート",
    "btn_new_note": "新規メモ",
    "note_title_ph": "メモのタイトル…",
    "note_content_ph": "アイデア、調査、プロットの覚え書き、展開するシーン…",
    "note_insert_title": "カーソル位置の現在の章にメモを挿入",
    "note_insert": "原稿に挿入",
    "note_convert_title": "このメモを正式な章に変換",
    "note_convert": "章に変換",
    "note_empty": "まだメモがありません",
    "note_untitled": "無題のメモ",
    "note_unsaved": "未保存",
    "note_loading": "読み込み中…",
    "note_del_title": "メモを削除",
    "note_del_confirm": "ノートブックから \"{title}\" を削除しますか？",
    "note_convert_confirm": "このメモを正式な章に変換しますか？目次と book.yaml に追加されます。",
    "note_inserted": "章に挿入しました！",
    "btn_delete": "削除"
  },
  "ru": {
    "chapter": "Глава",
    "cancel": "Отмена",
    "loading": "Загрузка…",

    "focus_exit_title": "Выйти из режима концентрации (Esc)",
    "focus_toggle_font_title": "Переключить шрифт: Печатная машинка / С засечками",
    "focus_dim_off": "👁️ Фокус: Выкл",
    "focus_dim_sentence": "🎯 Фокус: Предложение",
    "focus_dim_paragraph": "📑 Фокус: Абзац",
    "focus_dim_title": "Переключить режим фокуса (Выкл / Предложение / Абзац как в iA Writer)",
    "focus_super_focus_title": "Переключить супер-фокус (центрированная прокрутка как в iA Writer)",
    "focus_toggle_super_focus_title": "Переключить режим супер-фокуса (центрированная непрерывная прокрутка)",
    "focus_super_focus_on": "🎯 Супер-фокус: Вкл",
    "focus_super_focus_off": "🎯 Супер-фокус: Выкл",
    "focus_select_chapter": "Выбрать главу",
    "focus_select_chapter_title": "Выбрать главу",
    "recents_prune_missing": "Очистить отсутствующие",
    "recents_prune_title": "Удалить из списка проекты, папки которых больше не существуют на диске",
    "recents_btn_clear_all": "Очистить всё",
    "recents_clear_confirm_title": "Очистить недавние проекты",
    "recents_clear_confirm_desc": "Удалить все недавние проекты из этого списка? (Файлы на диске удалены не будут).",
    "recent_missing_badge": "Не найдено",
    "recent_missing_title": "Книга не найдена",
    "recent_missing_desc": "Папка этой книги больше не существует на диске:",
    "recent_missing_prompt": "Удалить её из списка недавних проектов?",
    "recent_remove_btn": "Удалить из списка",

    "btn_new_project": "✨ Новая книга…",
    "btn_new_project_title": "Создать новый проект книги (папка и book.yaml)",
    "btn_open": "Открыть…",
    "btn_open_title": "Открыть папку с книгой или импортировать файл",
    "btn_new_chapter": "＋ Новая глава",
    "btn_new_chapter_title": "Новая глава в открытой книге",
    "btn_style": "Стиль: ",
    "btn_style_title": "Стиль книги: применяется к печати, PDF и просмотру",
    "btn_opening": "Начало: ",
    "btn_opening_title": "Начало главы: буквица, капитель или без отступа",
    "btn_watch": "👁️ Live Watch",
    "btn_watch_title": "Открыть синхронный просмотр печати в реальном времени",
    "btn_kindle": "Kindle EPUB",
    "btn_kindle_title": "Создать EPUB для Kindle (KDP)",
    "btn_print": "Печать / PDF…",
    "btn_print_title": "Создать print.html и PDF для печати",
    "save_ready": "Готово",
    "save_saving": "Сохранение…",
    "save_saved": "Сохранено ",
    "save_error": "Ошибка сохранения",
    "conflict_detected": "Обнаружен конфликт",
    "save_failed": "Ошибка сохранения",
    "save_failed_msg": "Не удалось сохранить текущую главу перед продолжением.\nВведенный текст сохранен.",
    "save_failed_change_ch": "Не удалось сохранить текущую главу. Введенный текст сохранен.\nПожалуйста, устраните ошибку перед сменой главы.",
    "btn_retry_save": "Повторить сохранение",
    "btn_retry": "Повторить",
    "covers_header": "Обложки",
    "btn_add_cover": "＋ Добавить…",
    "untitled": "Без названия",
    "empty_title": "Начать новую книгу",
    "empty_desc": "Вы можете создать новый проект с нуля или открыть существующую папку (хранилище Markdown).",
    "btn_create_new_empty": "✨ Создать новую книгу…",
    "btn_open_folder_empty": "📁 Открыть папку…",
    "words_count": "{count} слов",
    "words_short": "слов",
    "vw_editor": "Редактор",
    "vw_split": "Редактор + Просмотр",
    "vw_preview": "Просмотр",
    "editor_placeholder": "Пишите здесь… автосохранение включено.",
    "open_modal_title": "Открыть или создать книгу",
    "open_choice_new": "✨ Создать новую книгу с нуля",
    "open_choice_folder": "📁 Папка с книгой (хранилище Markdown)",
    "open_choice_archive": "📕 Импортировать архив проекта → преобразовать в Markdown",
    "open_choice_docx": "📄 Импортировать рукопись (.docx, .vellum, .md) → в книгу",
    "btn_cancel": "Отмена",
    "np_modal_title": "✨ Создать новый проект книги",
    "np_desc": "Будет создана отдельная папка (хранилище Markdown) с book.yaml, начальной главой и папками для обложек и изображений.",
    "np_book_title_label": "Название книги *",
    "np_book_title_ph": "напр. Тайна реки",
    "np_author_label": "Имя автора",
    "np_author_ph": "напр. Иван Иванов",
    "np_parent_dir_label": "Родительская папка (где сохранить проект)",
    "np_parent_dir_ph": "Выберите папку...",
    "btn_browse": "Обзор…",
    "np_folder_name_label": "Имя папки",
    "np_folder_name_ph": "напр. the-rivers-secret",
    "np_initial_style_label": "Начальный стиль верстки",
    "btn_create_project": "Создать проект",
    "fs_modal_title": "Выбрать…",
    "fs_home": "🏠 Главная",
    "fs_volumes": "📂 Тома",
    "fs_up": "↑ Вверх",
    "fs_new_folder": "Новая папка…",
    "fs_select": "Выбрать",
    "fs_select_file": "Выбрать файл",
    "fs_select_folder": "Выбрать папку",
    "fs_select_files_count": "Выбрать {n} файлов",
    "fs_open_folder": "📁 Открыть эту папку",
    "fs_new_folder_prompt": "Имя новой папки:",
    "btn_report_issue": "Сообщить об ошибке",
    "btn_report_issue_title": "Сообщить о проблеме или просмотреть диагностику",
    "report_modal_title": "🛠️ Сообщить об ошибке",
    "report_privacy_notice": "Подготавливается локальный диагностический отчёт с версией, средой и ошибками. Текст рукописи, главы, PDF и личные данные не прикрепляются и не отправляются автоматически.",
    "report_desc_label": "Опишите ваши действия или возникшую неполадку *",
    "report_desc_ph": "Например: Попытка экспорта печатного PDF завершилась сбоем...",
    "report_expected_label": "Ожидаемый результат (необязательно)",
    "report_observed_label": "Фактический результат (необязательно)",
    "report_recent_errors_label": "Недавно зафиксированные системные ошибки:",
    "report_no_recent_errors": "Недавних ошибок не зарегистрировано.",
    "report_preview_title": "Предпросмотр отчёта:",
    "report_btn_prepare": "Подготовить отчёт",
    "report_btn_copy": "📋 Копировать отчёт",
    "report_btn_export": "💾 Экспорт в файл…",
    "report_copied_msg": "Скопировано в буфер обмена!",
    "empty_folder": "Папка пуста",
    "print_modal_title": "PDF / Печать — Настройки",
    "print_modal_desc": "Стиль книги (сохранен в хранилище, применяется к печати, PDF и просмотру):",
    "style_label": "Стиль",
    "body_font_label": "Шрифт текста",
    "scene_break_label": "Разделители сцен",
    "opening_label": "Начало главы",
    "opening_dropcap": "Буквица",
    "opening_smallcaps": "Капитель (Adelphi/Penguin)",
    "opening_none": "Без отступа",
    "trim_label": "Формат страницы",
    "pages_label": "Примерно страниц",
    "pages_est_ph": "≈ {pages} ({words} слов)",
    "bleed_label": "под обрез (Bleed)",
    "preview_chapter_label": "Глава для просмотра:",
    "open_full_viewprint": "📖 Просмотреть всю сверстанную книгу",
    "dest_folder_lbl_html": "Назначение: <b>out-stampa/</b> (в папке книги)",
    "dest_folder_custom": "Назначение: {dir}",
    "btn_pick_other_dir": "Выбрать другую папку…",
    "kdp_proof_note": "Окончательная проверка — в KDP Print Previewer.",
    "btn_start_build": "Запустить сборку",
    "preview_no_text": "В этой главе пока нет текста.",
    "preview_empty": "В этой главе пока нет текста.",
    "no_chapters_yet": "Нет глав",
    "new_chapter_title": "Добавить в книгу",
    "nc_type_label": "Тип раздела",
    "nc_type_chapter": "📖 Стандартная глава",
    "nc_type_dedication": "📜 Посвящение (начало книги)",
    "nc_type_epigraph": "✒️ Эпиграф (начало книги)",
    "nc_type_preface": "📑 Предисловие / Введение (начало)",
    "nc_type_acknowledgments": "🙏 Благодарности (конец книги)",
    "nc_type_about": "👤 Об авторе (конец книги)",
    "nc_title_label": "Название",
    "nc_title_ph": "напр. Глава 1",
    "nc_position_label": "Позиция",
    "nc_pos_after": "Сразу после выбранной главы",
    "nc_pos_end": "В конец книги",
    "nc_pos_start": "В начало книги",
    "btn_add": "Добавить",
    "default_dedication": "Посвящение",
    "default_epigraph": "Эпиграф",
    "default_about_author": "Об авторе",
    "default_acknowledgments": "Благодарности",
    "default_preface": "Предисловие",
    "modal_close": "Закрыть",
    "modal_error": "Ошибка",
    "build_running_title": "Сборка…",
    "build_running_desc": "Пожалуйста подождите, идет компиляция pubcraft…",
    "build_pdf_success": "PDF успешно создан ✔",
    "build_success": "Готово ✔",
    "btn_open_pdf": "Открыть PDF 📄",
    "btn_print_browser": "Печать / Сохранить в PDF сейчас (Браузер) 🖨",
    "btn_show_in_finder": "Показать в Finder",
    "btn_show_in_folder": "Показать в папке",
    "unsaved_changes_title": "Несохраненные изменения",
    "no_book_found_title": "Книга не найдена",
    "no_book_found_desc": "Выбранная папка не содержит файлов Markdown (.md) или файла book.yaml.\n\nИспользуйте «Новая книга» для создания проекта.",
    "confirm_delete_chapter": "Удалить «{title}\\» из оглавления?\n(Файл на диске НЕ будет удален)",
    "cover_set_as": "Сделать обложкой книги",
    "cover_delete": "Удалить это изображение из папки обложек?",
    "importing_doc": "Импорт рукописи…",
    "importing_wait": "Конвертация {dir}\nПожалуйста подождите…",
    "importing_book": "Импорт книги…",
    "importing_book_wait": "Распаковка {dir}\nПожалуйста подождите…",
    "import_failed": "Ошибка импорта",
    "sb_asterisks": "Звездочки (* * *)",
    "sb_fleuron": "Флерон (виньетка)",
    "sb_lozenge": "Ромб",
    "sb_line": "Линия с ромбом",
    "order_saved": "Порядок сохранен",
    "loading_chapter": "Загрузка главы…",
    "opened_time": "Открыто в {time}",
    "load_error": "Ошибка загрузки",
    "editing": "Редактирование…",
    "publish_ok": "Сборка завершена",
    "build_error": "Ошибка сборки",
    "created_time": "Создано в {time}",
    "pick_docx_title": "Выберите рукопись (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "Выберите архив для преобразования в Markdown",
    "pick_vault_title": "Выберите папку с книгой (хранилище)",
    "pick_print_dir_title": "Папка для сохранения print.html и PDF",
    "pick_cover_title": "Выберите файл обложки для копирования в cover/",
    "pick_np_dir_title": "Выберите место сохранения проекта книги",
    "enter_book_title": "Пожалуйста, введите название книги.",
    "select_project_folder": "Пожалуйста, выберите папку для сохранения проекта.",
    "enter_section_title": "Пожалуйста, введите название раздела.",
    "move_to_trash_confirm": "Переместить «{name}» в корзину (.cestina)?",
    "use_as_cover_confirm": "Использовать «{name}» как обложку EPUB?\n\n(Файл будет скопирован в images/ и записан в book.yaml)",
    "cover_epub_img_note": "Для обложки EPUB требуется изображение (JPEG/PNG). KDP рекомендует JPEG/TIFF с длинной стороной 2560px.",
    "project_creating_title": "Создание проекта…",
    "project_creating_wait": "Создание папки книги и файла book.yaml…",
    "btn_pub_preview": "📖 Предпросмотр…",
    "btn_pub_preview_title": "Открыть предпросмотр публикации (реальный EPUB и PDF для печати)",
    "pub_preview_title": "Предпросмотр публикации",
    "pub_target_print": "PDF для печати",
    "pub_target_ebook": "Электронная книга",
    "btn_pub_update": "Обновить предпросмотр",
    "pub_status_none": "Нет предпросмотра",
    "pub_status_generating": "Создание…",
    "pub_status_current": "Обновлено",
    "pub_status_stale": "Устарело",
    "pub_status_error": "Ошибка",
    "pub_download_file": "📥 Скачать файл",
    "pub_bleed_label": "Вылеты",
    "pub_view_label": "Вид:",
    "pub_view_single": "Одна страница",
    "pub_view_facing": "Разворот",
    "pub_guides_label": "Направляющие:",
    "pub_guide_cut": "Обрезка",
    "pub_guide_safe": "Безопасная зона",
    "pub_profile_label": "Профиль:",
    "pub_profile_reader": "Ридер",
    "pub_profile_tablet": "Планшет",
    "pub_profile_phone": "Смартфон",
    "pub_orient_label": "Ориентация:",
    "pub_orient_portrait": "Книжная",
    "pub_orient_landscape": "Альбомная",
    "pub_text_size_label": "Текст:",
    "pub_font_reset": "Сброс",
    "pub_kindle_disclaimer": "Отображение зависит от читалки. Для Kindle проверьте также в Kindle Previewer.",
    "pub_nav_jump_label": "Перейти:",
    "pub_nav_prev": "◀ Назад",
    "pub_nav_next": "Вперед ▶",
    "pub_zoom_label": "Масштаб:",
    "pub_zoom_fit_width": "По ширине",
    "pub_zoom_fit_page": "Вся страница",
    "pub_empty_title": "Предпросмотр еще не создан",
    "pub_prompt_update": "Нажмите «Обновить предпросмотр» для компиляции и просмотра реального документа.",
    "pub_loading_desc": "Компиляция реальной верстки и отображение сгенерированного файла.",
    "pub_error_title": "Ошибка создания предпросмотра",
    "pub_page_indicator": "Стр. {p} из {total}",
    "pub_recto": "Нечетная (Справа)",
    "pub_verso": "Четная (Слева)",
    "tb_bold": "Жирный (Cmd+B)",
    "tb_italic": "Курсив (Cmd+I)",
    "tb_h2": "Подзаголовок (H2)",
    "tb_h3": "Раздел (H3)",
    "tb_quote": "Цитата (>)",
    "tb_ul": "Маркированный список (-)",
    "tb_ol": "Нумерованный список (1.)",
    "tb_break": "Разделитель сцены (***)",
    "tb_link": "Вставить ссылку (Cmd+K)",
    "tb_image": "Вставить изображение",
    "tb_table": "Вставить таблицу",
    "table_modal_title": "Вставить таблицу",
    "table_cols": "Столбцы:",
    "table_rows": "Строки данных:",
    "table_insert": "Вставить",
    "table_col_prefix": "Столбец",
    "table_cell_prefix": "Ячейка",
    "welcome_version": "Версия 1.0.0-alpha.1",
    "welcome_tutorial_link": "Показать руководство PubCraft",
    "welcome_shortcuts_link": "Справка и сочетания клавиш",
    "welcome_btn_new": "✨ Новая книга…",
    "welcome_btn_docx": "📄 Импортировать рукопись (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 Открыть существующую папку…",
    "recents_header": "Недавние файлы",
    "recents_search_placeholder": "Фильтр проектов…",
    "recents_workspace_label": "Рабочая папка:",
    "recents_change_workspace": "Изменить…",
    "recents_all_tab": "Все недавние",
    "recents_workspace_tab": "В рабочей папке",
    "recents_empty_title": "Нет недавних книг",
    "recents_empty_desc": "Создайте новую книгу или откройте папку, чтобы начать.",
    "recents_btn_open_other": "Открыть другое…",
    "recents_btn_open_selected": "Открыть выбранное",
    "recents_remove_tooltip": "Удалить из недавних (не удаляет файлы на диске)",
    "topbar_recents": "📚 Недавние проекты",
    "topbar_recents_title": "Открыть недавние проекты и меню запуска",
    "topbar_close_book": "✕ Закрыть книгу",
    "topbar_close_book_title": "Закрыть текущую книгу",
    "fs_workspace": "⭐ Рабочая папка",
    "fs_documents": "📄 Документы",
    "fs_desktop": "🖥️ Рабочий стол",
    "fs_downloads": "📥 Загрузки",
    "fs_filter_placeholder": "Фильтр папок…",
    "fs_set_workspace_btn": "📌 Назначить рабочей папкой по умолчанию",
    "fs_workspace_saved": "Рабочая папка обновлена",
    "tutorial_modal_title": "Руководство и справка PubCraft",
    "tut_tab_guide": "Руководство",
    "tut_tab_shortcuts": "Сочетания клавиш",
    "topbar_title_tooltip": "Нажмите, чтобы переименовать книгу",
    "notebook_header": "Блокнот",
    "notebook_section_tooltip": "Заметки и идеи для этой книги",
    "btn_add_note": "＋ Новая заметка…",
    "btn_toggle_notebook": "📓 Заметки",
    "btn_focus_mode": "Фокус",
    "btn_focus_mode_title": "Полноэкранный режим концентрации (F11 или Esc для выхода)",
    "focus_exit": "Выход",
    "focus_font_typewriter": "⌨️ Печатная машинка",
    "focus_font_serif": "📖 Шрифт с засечками",
    "theme_dark": "Тёмная",
    "theme_light": "Светлая",
    "theme_toggle_title": "Переключить тёмную / светлую тему",
    "notebook_drawer_title": "Блокнот книги",
    "btn_new_note": "Новая заметка",
    "note_title_ph": "Название заметки…",
    "note_content_ph": "Идеи, исследования, детали сюжета, сцены для развития…",
    "note_insert_title": "Вставить текст заметки в текущую главу на курсор",
    "note_insert": "Вставить в рукопись",
    "note_convert_title": "Преобразовать заметку в официальную главу",
    "note_convert": "Преобразовать в главу",
    "note_empty": "Пока нет заметок",
    "note_untitled": "Заметка без названия",
    "note_unsaved": "Не сохранено",
    "note_loading": "Загрузка…",
    "note_del_title": "Удалить заметку",
    "note_del_confirm": "Удалить заметку \"{title}\" из блокнота?",
    "note_convert_confirm": "Преобразовать эту заметку в официальную главу? Она будет добавлена в оглавление и book.yaml.",
    "note_inserted": "Вставлено в главу!",
    "btn_delete": "Удалить"
  },
  "ar": {
    "chapter": "الفصل",
    "cancel": "إلغاء",
    "loading": "جاري التحميل…",

    "focus_exit_title": "الخروج من وضع التركيز (Esc)",
    "focus_toggle_font_title": "تبديل الخط: آلة كاتبة / Serif",
    "focus_dim_off": "👁️ تركيز: متوقف",
    "focus_dim_sentence": "🎯 تركيز: جملة",
    "focus_dim_paragraph": "📑 تركيز: فقرة",
    "focus_dim_title": "تبديل وضع التركيز (متوقف / جملة / فقرة مثل iA Writer)",
    "focus_super_focus_title": "تبديل وضع التركيز الفائق (تمرير في المنتصف مثل iA Writer)",
    "focus_toggle_super_focus_title": "تبديل التركيز الفائق (تمرير مستمر في المنتصف)",
    "focus_super_focus_on": "🎯 تركيز فائق: مفعّل",
    "focus_super_focus_off": "🎯 تركيز فائق: معطّل",
    "focus_select_chapter": "اختر فصلاً",
    "focus_select_chapter_title": "اختر فصلاً",
    "recents_prune_missing": "تنظيف المفقودة",
    "recents_prune_title": "إزالة مجلدات المشاريع التي لم تعد موجودة على القرص",
    "recents_btn_clear_all": "مسح الكل",
    "recents_clear_confirm_title": "مسح المشاريع الأخيرة",
    "recents_clear_confirm_desc": "هل تريد إزالة جميع المشاريع الأخيرة من هذه القائمة؟ (لن يتم حذف ملفاتك على القرص).",
    "recent_missing_badge": "مفقود",
    "recent_missing_title": "لم يتم العثور على الكتاب",
    "recent_missing_desc": "مجلد هذا الكتاب لم يعد موجوداً على القرص:",
    "recent_missing_prompt": "هل تريد إزالته من المشاريع الأخيرة؟",
    "recent_remove_btn": "إزالة من الأخيرة",

    "btn_new_project": "✨ كتاب جديد…",
    "btn_new_project_title": "إنشاء مشروع كتاب جديد (مجلد وملف book.yaml)",
    "btn_open": "فتح…",
    "btn_open_title": "فتح مجلد كتاب موجود أو استيراد ملف",
    "btn_new_chapter": "＋ فصل جديد",
    "btn_new_chapter_title": "فصل جديد في الكتاب المفتوح",
    "btn_style": "النمط: ",
    "btn_style_title": "نمط الكتاب: يسري على الطباعة، ملفات PDF والمعاينة",
    "btn_opening": "البداية: ",
    "btn_opening_title": "بداية الفصل: حرف استهلالي، حروف صغيرة كبيرة أو عادي",
    "btn_watch": "👁️ المعاينة المباشرة",
    "btn_watch_title": "فتح معاينة الطباعة المتزامنة في الوقت الفعلي",
    "btn_kindle": "Kindle EPUB",
    "btn_kindle_title": "إنشاء ملف EPUB لـ Kindle (KDP)",
    "btn_print": "طباعة / PDF…",
    "btn_print_title": "إنشاء print.html و PDF للطباعة",
    "save_ready": "جاهز",
    "save_saving": "جارٍ الحفظ…",
    "save_saved": "تم الحفظ ",
    "save_error": "خطأ في الحفظ",
    "conflict_detected": "تم اكتشاف تعارض",
    "save_failed": "فشل الحفظ",
    "save_failed_msg": "تعذر حفظ الفصل الحالي قبل المتابعة.\nتم الحفاظ على النص المكتوب.",
    "save_failed_change_ch": "تعذر حفظ الفصل الحالي. تم الحفاظ على النص.\nيرجى معالجة الخطأ قبل تبديل الفصل.",
    "btn_retry_save": "إعادة محاولة الحفظ",
    "btn_retry": "إعادة المحاولة",
    "covers_header": "الأغلفة",
    "btn_add_cover": "＋ إضافة…",
    "untitled": "بلا عنوان",
    "empty_title": "ابدأ كتابًا جديدًا",
    "empty_desc": "يمكنك إنشاء مشروع جديد من الصفر أو فتح مجلد موجود (Markdown vault).",
    "btn_create_new_empty": "✨ إنشاء كتاب جديد…",
    "btn_open_folder_empty": "📁 فتح مجلد موجود…",
    "words_count": "{count} كلمة",
    "words_short": "كلمات",
    "vw_editor": "المحرر",
    "vw_split": "محرر + معاينة",
    "vw_preview": "معاينة",
    "editor_placeholder": "اكتب هنا… الحفظ التلقائي مفعّل.",
    "open_modal_title": "فتح أو إنشاء كتاب",
    "open_choice_new": "✨ إنشاء كتاب جديد من الصفر",
    "open_choice_folder": "📁 مجلد كتاب موجود (Markdown vault)",
    "open_choice_archive": "📕 استيراد أرشيف المشروع ← تحويل إلى Markdown",
    "open_choice_docx": "📄 استيراد مخطوطة (.docx, .vellum, .md) ← تحويل إلى كتاب",
    "btn_cancel": "إلغاء",
    "np_modal_title": "✨ إنشاء مشروع كتاب جديد",
    "np_desc": "سيتم إنشاء مجلد مخصص (Markdown vault) جاهز للكتابة، مع book.yaml وفصل أولي ومجلدات للأغلفة والصور.",
    "np_book_title_label": "عنوان الكتاب *",
    "np_book_title_ph": "مثال: سر النهر",
    "np_author_label": "اسم المؤلف",
    "np_author_ph": "مثال: أحمد علي",
    "np_parent_dir_label": "المجلد الرئيسي (مكان حفظ المشروع)",
    "np_parent_dir_ph": "اختر المجلد...",
    "btn_browse": "تصفح…",
    "np_folder_name_label": "اسم المجلد",
    "np_folder_name_ph": "مثال: the-rivers-secret",
    "np_initial_style_label": "النمط التحريري الأولي",
    "btn_create_project": "إنشاء المشروع",
    "fs_modal_title": "اختر…",
    "fs_home": "🏠 الرئيسية",
    "fs_volumes": "📂 وحدات التخزين",
    "fs_up": "↑ للأعلى",
    "fs_new_folder": "مجلد جديد…",
    "fs_select": "اختيار",
    "fs_select_file": "اختيار الملف",
    "fs_select_folder": "اختيار المجلد",
    "fs_select_files_count": "اختيار {n} ملفات",
    "fs_open_folder": "📁 فتح هذا المجلد",
    "fs_new_folder_prompt": "اسم المجلد الجديد:",
    "btn_report_issue": "الإبلاغ عن مشكلة",
    "btn_report_issue_title": "الإبلاغ عن مشكلة أو عرض التشخيص",
    "report_modal_title": "🛠️ الإبلاغ عن مشكلة",
    "report_privacy_notice": "يتم إعداد تقرير تشخيصي محلي يحتوي على الإصدار والبيئة والأخطاء المسجلة. لا يتم إرفاق أو إرسال أي نصوص من المخطوطة أو الفصول أو ملفات PDF أو بيانات شخصية تلقائيًا.",
    "report_desc_label": "صف ما كنت تفعله أو ما لا يعمل *",
    "report_desc_ph": "مثال: حاولت تصدير ملف PDF للطباعة لكن العملية فشلت...",
    "report_expected_label": "النتيجة المتوقعة (اختياري)",
    "report_observed_label": "النتيجة الفعلية (اختياري)",
    "report_recent_errors_label": "أخطاء النظام الأخيرة المسجلة:",
    "report_no_recent_errors": "لم يتم تسجيل أي أخطاء حديثة.",
    "report_preview_title": "معاينة التقرير الذي تم إنشاؤه:",
    "report_btn_prepare": "إعداد التقرير",
    "report_btn_copy": "📋 نسخ التقرير",
    "report_btn_export": "💾 تصدير إلى ملف…",
    "report_copied_msg": "تم النسخ إلى الحافظة!",
    "empty_folder": "مجلد فارغ",
    "print_modal_title": "PDF / طباعة — الإعدادات",
    "print_modal_desc": "نمط الكتاب (محفوظ في الـ vault، يسري على الطباعة، PDF والمعاينة):",
    "style_label": "النمط",
    "body_font_label": "خط النص",
    "scene_break_label": "فواصل المشاهد",
    "opening_label": "بداية الفصل",
    "opening_dropcap": "حرف استهلالي كبير",
    "opening_smallcaps": "حروف صغيرة كبيرة (Adelphi/Penguin)",
    "opening_none": "بسيط ومحاذى",
    "trim_label": "حجم الورق",
    "pages_label": "الصفحات التقديرية",
    "pages_est_ph": "≈ {pages} ({words} كلمة)",
    "bleed_label": "نزيف كامل (Bleed)",
    "preview_chapter_label": "فصل المعاينة:",
    "open_full_viewprint": "📖 عرض الكتاب كاملاً بعد التنسيق",
    "dest_folder_lbl_html": "الوجهة: <b>out-stampa/</b> (داخل مجلد الكتاب)",
    "dest_folder_custom": "الوجهة: {dir}",
    "btn_pick_other_dir": "اختر مجلدًا آخر…",
    "kdp_proof_note": "المعاينة النهائية تبقى عبر KDP Print Previewer.",
    "btn_start_build": "بدء الإنشاء",
    "preview_no_text": "لا يوجد نص في هذا الفصل بعد.",
    "preview_empty": "هذا الفصل فارغ.",
    "no_chapters_yet": "لا توجد فصول",
    "new_chapter_title": "إضافة إلى الكتاب",
    "nc_type_label": "نوع القسم",
    "nc_type_chapter": "📖 فصل عادي",
    "nc_type_dedication": "📜 إهداء (بداية الكتاب)",
    "nc_type_epigraph": "✒️ مقولة استهلالية (بداية الكتاب)",
    "nc_type_preface": "📑 مقدمة / تمهيد (بداية الكتاب)",
    "nc_type_acknowledgments": "🙏 شكر وتقدير (نهاية الكتاب)",
    "nc_type_about": "👤 عن المؤلف (نهاية الكتاب)",
    "nc_title_label": "العنوان",
    "nc_title_ph": "مثال: الفصل 1",
    "nc_position_label": "الموضع",
    "nc_pos_after": "بعد الفصل المحدد مباشرة",
    "nc_pos_end": "في نهاية الكتاب",
    "nc_pos_start": "في بداية الكتاب",
    "btn_add": "إضافة",
    "default_dedication": "إهداء",
    "default_epigraph": "مقولة استهلالية",
    "default_about_author": "عن المؤلف",
    "default_acknowledgments": "شكر وتقدير",
    "default_preface": "مقدمة",
    "modal_close": "إغلاق",
    "modal_error": "خطأ",
    "build_running_title": "جارٍ الإنشاء…",
    "build_running_desc": "يرجى الانتظار، يتم التحويل الآن عبر pubcraft…",
    "build_pdf_success": "تم إنشاء ملف PDF بنجاح ✔",
    "build_success": "تم ✔",
    "btn_open_pdf": "فتح PDF 📄",
    "btn_print_browser": "طباعة / حفظ كـ PDF الآن (المتصفح) 🖨",
    "btn_show_in_finder": "إظهار في Finder",
    "btn_show_in_folder": "إظهار في المجلد",
    "unsaved_changes_title": "تغييرات غير محفوظة",
    "no_book_found_title": "لم يتم العثور على كتاب",
    "no_book_found_desc": "المجلد المحدد لا يحتوي على ملفات Markdown (.md) أو ملف book.yaml.\n\nاستخدم 'كتاب جديد' لإنشاء مشروع جديد.",
    "confirm_delete_chapter": "إزالة \"{title}\" من جدول الفصول؟\n(لن يتم حذف الملف من القرص)",
    "cover_set_as": "تعيين كغلاف للكتاب",
    "cover_delete": "حذف هذه الصورة من مجلد الأغلفة؟",
    "importing_doc": "جارٍ استيراد المخطوطة…",
    "importing_wait": "تحويل {dir}\nيرجى الانتظار…",
    "importing_book": "جارٍ استيراد الكتاب…",
    "importing_book_wait": "فك ضغط {dir}\nيرجى الانتظار…",
    "import_failed": "فشل الاستيراد",
    "sb_asterisks": "نجوم (* * *)",
    "sb_fleuron": "زخرفة فلورون",
    "sb_lozenge": "معين",
    "sb_line": "خط مع معين",
    "order_saved": "تم حفظ الترتيب",
    "loading_chapter": "جارٍ تحميل الفصل…",
    "opened_time": "تم الفتح في {time}",
    "load_error": "خطأ في التحميل",
    "editing": "تعديل…",
    "publish_ok": "تم النشر بنجاح",
    "build_error": "خطأ في الإنشاء",
    "created_time": "تم الإنشاء في {time}",
    "pick_docx_title": "اختر المخطوطة (.docx, .vellum, .md, .odt, .pdf)",
    "pick_archive_title": "اختر الأرشيف للتحويل إلى Markdown",
    "pick_vault_title": "اختر مجلد الكتاب (vault)",
    "pick_print_dir_title": "المجلد المخصص لحفظ print.html وملف PDF",
    "pick_cover_title": "اختر صورة الغلاف لنسخها إلى مجلد cover/",
    "pick_np_dir_title": "اختر مكان حفظ مشروع الكتاب",
    "enter_book_title": "يرجى إدخال عنوان للكتاب.",
    "select_project_folder": "يرجى تحديد المجلد لحفظ المشروع.",
    "enter_section_title": "يرجى إدخال عنوان للقسم.",
    "move_to_trash_confirm": "نقل \"{name}\" إلى سلة المهملات (.cestina)؟",
    "use_as_cover_confirm": "استخدام \"{name}\" كغلاف EPUB؟\n\n(سيتم نسخها إلى images/ وضبطها في book.yaml)",
    "cover_epub_img_note": "يتطلب غلاف EPUB ملف صورة (JPEG/PNG). توصي KDP بصور JPEG/TIFF ذات جانب أطول 2560 بكسل.",
    "project_creating_title": "جارٍ إنشاء المشروع…",
    "project_creating_wait": "إنشاء مجلد الكتاب وملف book.yaml…",
    "btn_pub_preview": "📖 معاينة النشر…",
    "btn_pub_preview_title": "فتح معاينة النشر (ملف EPUB و PDF حقيقي)",
    "pub_preview_title": "معاينة النشر",
    "pub_target_print": "طباعة PDF",
    "pub_target_ebook": "كتاب إلكتروني",
    "btn_pub_update": "تحديث المعاينة",
    "pub_status_none": "لا توجد معاينة",
    "pub_status_generating": "جاري الإنشاء…",
    "pub_status_current": "محدثة",
    "pub_status_stale": "بحاجة لتحديث",
    "pub_status_error": "خطأ",
    "pub_download_file": "📥 تنزيل الملف",
    "pub_bleed_label": "هوامش التسييل",
    "pub_view_label": "العرض:",
    "pub_view_single": "صفحة مفردة",
    "pub_view_facing": "صفحتان متقابلتان",
    "pub_guides_label": "الإرشادات:",
    "pub_guide_cut": "القص",
    "pub_guide_safe": "المنطقة الآمنة",
    "pub_profile_label": "الجهاز:",
    "pub_profile_reader": "قارئ إلكتروني",
    "pub_profile_tablet": "جهاز لوحي",
    "pub_profile_phone": "هاتف ذكي",
    "pub_orient_label": "الاتجاه:",
    "pub_orient_portrait": "عمودي",
    "pub_orient_landscape": "أفقي",
    "pub_text_size_label": "الخط:",
    "pub_font_reset": "إعادة ضبط",
    "pub_kindle_disclaimer": "قد يختلف العرض باختلاف تطبيق القراءة. بالنسبة لأجهزة Kindle، تحقق أيضاً عبر Kindle Previewer.",
    "pub_nav_jump_label": "انتقال إلى:",
    "pub_nav_prev": "◀ السابق",
    "pub_nav_next": "التالي ▶",
    "pub_zoom_label": "تكبير:",
    "pub_zoom_fit_width": "ملء العرض",
    "pub_zoom_fit_page": "ملء الصفحة",
    "pub_empty_title": "لم يتم إنشاء معاينة بعد",
    "pub_prompt_update": "انقر فوق 'تحديث المعاينة' لتجميع المستند الحقيقي وعرضه.",
    "pub_loading_desc": "جاري تجميع التنسيق الدقيق وعرض الملف المُنتج.",
    "pub_error_title": "خطأ في إنشاء المعاينة",
    "pub_page_indicator": "صفحة {p} من {total}",
    "pub_recto": "يمين (Recto)",
    "pub_verso": "يسار (Verso)",
    "tb_bold": "عريض (Cmd+B)",
    "tb_italic": "مائل (Cmd+I)",
    "tb_h2": "عنوان فرعي (H2)",
    "tb_h3": "قسم فرعي (H3)",
    "tb_quote": "اقتباس (>)",
    "tb_ul": "قائمة نقطية (-)",
    "tb_ol": "قائمة مرقمة (1.)",
    "tb_break": "فاصل المشهد (***)",
    "tb_link": "إدراج رابط (Cmd+K)",
    "tb_image": "إدراج صورة",
    "tb_table": "إدراج جدول",
    "table_modal_title": "إدراج جدول",
    "table_cols": "الأعمدة:",
    "table_rows": "صفوف البيانات:",
    "table_insert": "إدراج",
    "table_col_prefix": "عمود",
    "table_cell_prefix": "خلية",
    "welcome_version": "الإصدار 1.0.0-alpha.1",
    "welcome_tutorial_link": "عرض دليل PubCraft التعليمي",
    "welcome_shortcuts_link": "المساعدة واختصارات لوحة المفاتيح",
    "welcome_btn_new": "✨ كتاب جديد…",
    "welcome_btn_docx": "📄 استيراد مخطوطة (.docx, .vellum, .md)…",
    "welcome_btn_browse": "📁 فتح مجلد موجود…",
    "recents_header": "الملفات الأخيرة",
    "recents_search_placeholder": "تصفية المشاريع…",
    "recents_workspace_label": "مساحة العمل:",
    "recents_change_workspace": "تغيير…",
    "recents_all_tab": "كل الأخيرة",
    "recents_workspace_tab": "في مساحة العمل",
    "recents_empty_title": "لا توجد كتب حديثة",
    "recents_empty_desc": "أنشئ كتابًا جديدًا أو افتح مجلدًا للبدء في الكتابة.",
    "recents_btn_open_other": "فتح مسار آخر…",
    "recents_btn_open_selected": "فتح المحدد",
    "recents_remove_tooltip": "إزالة من القائمة الأخيرة (لا يحذف الملفات من القرص)",
    "topbar_recents": "📚 المشاريع الأخيرة",
    "topbar_recents_title": "فتح المشاريع الأخيرة وشاشة البدء",
    "topbar_close_book": "✕ إغلاق الكتاب",
    "topbar_close_book_title": "إغلاق الكتاب الحالي",
    "fs_workspace": "⭐ مساحة العمل",
    "fs_documents": "📄 المستندات",
    "fs_desktop": "🖥️ سطح المكتب",
    "fs_downloads": "📥 التنزيلات",
    "fs_filter_placeholder": "تصفية المجلدات…",
    "fs_set_workspace_btn": "📌 تعيين كمساحة عمل افتراضية",
    "fs_workspace_saved": "تم تحديث مساحة العمل",
    "tutorial_modal_title": "دليل وشروحات PubCraft",
    "tut_tab_guide": "الدليل التعليمي",
    "tut_tab_shortcuts": "الاختصارات",
    "topbar_title_tooltip": "انقر لإعادة تسمية الكتاب",
    "notebook_header": "دفتر الملاحظات",
    "notebook_section_tooltip": "ملاحظات وأفكار لهذا الكتاب",
    "btn_add_note": "＋ ملاحظة جديدة…",
    "btn_toggle_notebook": "📓 الملاحظات",
    "btn_focus_mode": "وضع التركيز",
    "btn_focus_mode_title": "وضع التركيز الخالي من المشتتات / ملء الشاشة (F11 أو Esc للخروج)",
    "focus_exit": "خروج",
    "focus_font_typewriter": "⌨️ آلة كاتبة",
    "focus_font_serif": "📖 خط Serif",
    "theme_dark": "داكن",
    "theme_light": "فاتح",
    "theme_toggle_title": "تبديل المظهر الداكن / الفاتح",
    "notebook_drawer_title": "دفتر ملاحظات الكتاب",
    "btn_new_note": "ملاحظة جديدة",
    "note_title_ph": "عنوان الملاحظة…",
    "note_content_ph": "أفكار، أبحاث، تذكيرات بالحبكة، مشاهد لتطويرها…",
    "note_insert_title": "إدراج نص الملاحظة في الفصل الحالي عند المؤشر",
    "note_insert": "إدراج في المخطوطة",
    "note_convert_title": "تحويل هذه الملاحظة إلى فصل رسمي في الكتاب",
    "note_convert": "تحويل إلى فصل",
    "note_empty": "لا توجد ملاحظات بعد",
    "note_untitled": "ملاحظة بدون عنوان",
    "note_unsaved": "غير محفوظ",
    "note_loading": "جارٍ التحميل…",
    "note_del_title": "حذف الملاحظة",
    "note_del_confirm": "هل أنت متأكد من حذف الملاحظة \"{title}\" من دفتر الملاحظات؟",
    "note_convert_confirm": "هل ترغب في تحويل هذه الملاحظة إلى فصل رسمي؟ ستُضاف إلى الفهرس وإلى book.yaml.",
    "note_inserted": "تم الإدراج في الفصل!",
    "btn_delete": "حذف"
  }
};

let currentLang = "en";

function t(key, params) {
  const dict = I18N[currentLang] || I18N["en"];
  let str = dict[key] || (I18N["en"] && I18N["en"][key]) || key;
  if (params && typeof params === "object") {
    for (const [k, v] of Object.entries(params)) {
      str = str.replace(new RegExp(`\\{${k}\\}`, "g"), v);
    }
  }
  return str;
}

function detectLanguage() {
  const saved = localStorage.getItem("pubcraft_lang");
  if (saved && I18N[saved]) return saved;
  const sys = (navigator.language || (navigator.languages && navigator.languages[0]) || "en").slice(0, 2).toLowerCase();
  if (I18N[sys]) return sys;
  return "en";
}

function updateWordCount(n) {
  if (n === undefined) {
    const val = (typeof ta !== "undefined" && ta) ? (ta.value || "") : "";
    const body = val.replace(/^---\n[\s\S]*?\n---\n/, "");
    n = (body.trim().match(/\S+/g) || []).length;
  }
  if ($("wordCount")) {
    $("wordCount").textContent = t("words_count", { count: n.toLocaleString(currentLang) });
  }
  if (typeof isFocusMode !== "undefined" && isFocusMode && $("focusWordCount")) {
    $("focusWordCount").textContent = $("wordCount") ? $("wordCount").textContent : `${n}`;
  }
}

function setLanguage(lang) {
  if (!I18N[lang]) lang = "en";
  currentLang = lang;
  document.documentElement.lang = lang;
  document.documentElement.dir = lang === "ar" ? "rtl" : "ltr";

  document.querySelectorAll("[data-i18n]").forEach((el) => {
    const k = el.getAttribute("data-i18n");
    const val = t(k);
    if (val) el.textContent = val;
  });

  document.querySelectorAll("[data-i18n-title]").forEach((el) => {
    const k = el.getAttribute("data-i18n-title");
    const val = t(k);
    if (val) el.title = val;
  });

  document.querySelectorAll("[data-i18n-placeholder]").forEach((el) => {
    const k = el.getAttribute("data-i18n-placeholder");
    const val = t(k);
    if (val) el.placeholder = val;
  });

  styleLabel();
  openingLabel();
  updateWordCount();
  if ($("tutorialModal") && !$("tutorialModal").classList.contains("hidden")) {
    showTutorialModal(activeTutorialTab);
  }
  renderRecentList();

  const sbSel = $("sceneBreak");
  if (sbSel && sbSel.options.length >= 4) {
    const curVal = sbSel.value;
    sbSel.options[0].text = t("sb_asterisks");
    sbSel.options[1].text = t("sb_fleuron");
    sbSel.options[2].text = t("sb_lozenge");
    sbSel.options[3].text = t("sb_line");
    sbSel.value = curVal;
  }

  if (typeof updateThemeUI === "function") updateThemeUI();

  updatePrintDestinationLabel();

  if ($("langSelect") && $("langSelect").value !== lang) {
    $("langSelect").value = lang;
  }

  localStorage.setItem("pubcraft_lang", lang);
}

function updatePrintDestinationLabel() {
  const label = $("lblPrintOutDir");
  if (!label) return;
  if (customPrintDir) {
    label.textContent = t("dest_folder_custom", { dir: customPrintDir });
  } else {
    label.innerHTML = t("dest_folder_lbl_html");
  }
}

const fmtTime = () => new Date().toLocaleTimeString(currentLang, { hour: "2-digit", minute: "2-digit" });

function setState(msg, cls) {
  const el = $("saveState");
  el.textContent = msg;
  el.className = cls || "";
  if (typeof isFocusMode !== "undefined" && isFocusMode && $("focusSaveStatus")) {
    $("focusSaveStatus").textContent = msg;
    $("focusSaveStatus").className = "focus-hud-status " + (cls || "");
  }
}

/* ---------------------------------------------------------------- sidebar */

function renderBook() {
  const b = state.book;
  $("bookTitle").textContent = b ? (b.title || "Libro senza titolo") : "—";
  if ($("topbarBookTitle")) $("topbarBookTitle").textContent = b ? (b.title || "Libro senza titolo") : "—";
  if (b && b.title) document.title = `${b.title} — PubCraft`;
  chapterList.innerHTML = "";
  if (!b) return;
  b.chapters.forEach((c, i) => {
    const li = document.createElement("li");
    li.dataset.file = c.file;
    if (c.file === currentFile) li.classList.add("active");
    const num = document.createElement("span");
    num.className = "num";
    let icon = String(i + 1);
    if (c.type === "dedication") icon = "📜";
    else if (c.type === "epigraph") icon = "✒️";
    else if (c.type === "about-author") icon = "👤";
    else if (c.type === "acknowledgments") icon = "🙏";
    else if (c.type === "preface") icon = "📑";
    num.textContent = icon;
    const t = document.createElement("span");
    t.className = "t";
    t.textContent = c.title || c.file;
    const x = document.createElement("button");
    x.className = "x"; x.textContent = "✕"; x.title = "Sposta in .cestina";
    x.addEventListener("click", (e) => { e.stopPropagation(); deleteChapter(c); });
    li.append(num, t, x);
    li.addEventListener("click", () => openChapter(c.file));
    chapterList.appendChild(li);
  });
}

function updateDocVisibility() {
  const has = !!(state.vault && state.book && state.book.chapters);
  if ($("sidebar")) $("sidebar").style.display = has ? "flex" : "none";
  $("empty").style.display = has ? "none" : "flex";
  const hideDoc = !has || !currentFile;
  $("doc").hidden = hideDoc;
  $("doc").classList.toggle("hidden", hideDoc);
  $("doc").style.display = hideDoc ? "none" : "";
  $("coverSection").style.display = state.vault ? "block" : "none";
  if ($("notebookSection")) $("notebookSection").style.display = state.vault ? "block" : "none";

  // Contestualizzazione dei gruppi in Topbar (snelli, ordinati e mirati)
  if ($("groupWelcome")) $("groupWelcome").classList.toggle("hidden", has);
  if ($("groupEditing")) $("groupEditing").classList.toggle("hidden", !has);
  if ($("groupExport")) $("groupExport").classList.toggle("hidden", !has);
  if ($("saveState")) $("saveState").classList.toggle("hidden", !has);
  if ($("topbarBookHeader")) $("topbarBookHeader").classList.toggle("hidden", !has);
  if ($("btnCloseBook")) $("btnCloseBook").classList.toggle("hidden", !has);

  if (has && state.book && state.book.title && $("topbarBookTitle")) {
    $("topbarBookTitle").textContent = state.book.title;
  }

  if (!has) {
    if (ta) ta.value = "";
    currentFile = "";
    currentFileVault = "";
    if ($("preview")) $("preview").srcdoc = "";
    if ($("curName")) $("curName").textContent = "—";
    if ($("topbarBookTitle")) $("topbarBookTitle").textContent = "—";
    if ($("bookTitle")) $("bookTitle").textContent = "—";
    loadRecents();
  }
}

/* ---------------------------------------------------------------- loading */

let appPollStarted = false;
function startAppWatchSync() {
  if (appPollStarted) return;
  appPollStarted = true;
  setTimeout(() => {
    setInterval(async () => {
      if (!isBuilding && !isProjectSwitching) {
        const currentVault = (state && state.vault) || "";
        const prevModTime = (state && state.vaultModTime) || "";
        try {
          const headers = currentVault ? { "X-PubCraft-Vault": currentVault } : {};
          const res = await fetch("/api/vault-fingerprint", { headers });
          if (res.ok) {
            const data = await res.json();
            if (data && data.vault) {
              if (currentVault && data.vault === currentVault) {
                if (prevModTime && data.vaultModTime && data.vaultModTime !== prevModTime) {
                  markPublicationPreviewStale();
                }
                state.vaultModTime = data.vaultModTime;
              } else if (data.vault !== currentVault) {
                // Il libro/vault è cambiato sul server (es. da pubcraft_open_book via MCP)
                if (dirty) {
                  await flushSave();
                }
                await refreshState();
              }
            } else if (!data || !data.vault) {
              if (currentVault) {
                state.vault = "";
                await refreshState();
              }
            }
          }
        } catch (_) {}
      }
    }, 400);
  }, 800);
}

async function refreshState() {
  startAppWatchSync();
  const prevModTime = (state && state.vaultModTime) || "";
  state = await api("/api/state");
  if (prevModTime && state && state.vaultModTime && state.vaultModTime !== prevModTime) {
    markPublicationPreviewStale();
  }
  defaultParentDir = state.docsDir || state.homeDir || defaultParentDir;
  workspaceDir = state.workspaceDir || defaultParentDir;
  if (state && state.vault && state.book) {
    await refreshStyle();
    renderBook();
    loadNotes();
  }
  updateDocVisibility();
}

window.addEventListener("focus", async () => {
  try {
    const fresh = await api("/api/state");
    if (!fresh) return;
    const currentVault = (state && state.vault) || "";
    if (currentVault && fresh.vault === currentVault) {
      const prevModTime = (state && state.vaultModTime) || "";
      if (prevModTime && fresh.vaultModTime && fresh.vaultModTime !== prevModTime) {
        markPublicationPreviewStale();
      }
      state = fresh;
    } else if (fresh.vault !== currentVault) {
      if (dirty) {
        await flushSave();
      }
      await refreshState();
    }
  } catch (_) {}
});

async function flushSave() {
  clearTimeout(saveTimer);
  saveTimer = null;
  if (savePromise) {
    const pending = savePromise;
    const ok = await pending;
    if (savePromise === pending) savePromise = null;
    if (!ok && !dirty) return false;
  }
  if (!currentFile && (dirty || saveTimer || saveRevision > lastSavedRevision)) return false;
  while (dirty || (currentFile && saveRevision > lastSavedRevision)) {
    if (!currentFile) return false;
    const ok = await saveNow();
    if (!ok) return false;
  }
  return !dirty && (saveRevision <= lastSavedRevision);
}

async function flushBeforeVaultSwitch() {
  const hasPending = dirty || saveTimer || savePromise ||
    (currentFile && saveRevision > lastSavedRevision);
  if (!hasPending) return true;
  if (await flushSave()) return true;
  showModal(t("save_failed"), t("save_failed_change_ch"), [
    [t("btn_retry_save"), async () => { hideModal(); await flushBeforeVaultSwitch(); }],
    [t("btn_cancel"), hideModal],
  ]);
  return false;
}

async function openChapter(file, force = false) {
  if ((isProjectSwitching || isBuilding) && !force) return;
  if (currentFile && currentFile === file && !isLoadingChapter) return;
  if (dirty || saveTimer || savePromise || (currentFile && saveRevision !== lastSavedRevision)) {
    const ok = await flushSave();
    if (!ok) {
      showModal(t("save_failed"),
        t("save_failed_change_ch"),
        [[t("btn_retry_save"), async () => { hideModal(); await saveNow(); }], [t("btn_cancel"), hideModal]]);
      return;
    }
  }

  const thisNavId = ++chapterNavId;
  const targetVault = (state && state.vault) || "";
  isLoadingChapter = true;
  ta.readOnly = true;
  setState(t("loading_chapter"), "busy");

  try {
    const res = await api("/api/chapter?file=" + encodeURIComponent(file));
    if (thisNavId !== chapterNavId || targetVault !== ((state && state.vault) || "") || ((isProjectSwitching || isBuilding) && !force)) {
      return;
    }
    if (res.vault !== targetVault) throw new Error("Il progetto attivo è cambiato: riapri il libro prima di modificare il capitolo.");
    currentFile = file;
    currentFileVault = targetVault;
    ta.value = res.content || "";
    fileVersion = res.version;
    dirty = false;
    saveRevision = 0;
    lastSavedRevision = 0;
    notifyClientDirty();
    isLoadingChapter = false;
    ta.readOnly = ((isProjectSwitching || isBuilding) && !force) ? true : false;

    const c = state.book.chapters.find((x) => x.file === file);
    currentTitle = c ? (c.title || file) : file;
    $("curName").textContent = currentTitle;
    [...chapterList.children].forEach((li) =>
      li.classList.toggle("active", li.dataset.file === file));
    setState(t("opened_time", { time: fmtTime() }));
    updateWordCount();
    renderPreview();
    updateDocVisibility();
    if (typeof updateFocusHud === "function" && isFocusMode) updateFocusHud();
  } catch (err) {
    if (thisNavId === chapterNavId) {
      isLoadingChapter = false;
      setState(t("load_error") + ": " + err.message, "err");
    }
  } finally {
    if (thisNavId === chapterNavId) {
      isLoadingChapter = false;
      if (!isProjectSwitching && !isBuilding && currentFile) {
        ta.readOnly = false;
      } else {
        ta.readOnly = true;
      }
    }
  }
}

/* -------------------------------------------------------------- autosave */

function queueSave() {
  if (!currentFile || isLoadingChapter || isProjectSwitching || isBuilding || ta.readOnly) return;
  dirty = true;
  saveRevision++;
  notifyClientDirty();
  setState(t("editing"), "busy");
  clearTimeout(saveTimer);
  saveTimer = setTimeout(saveNow, 800);
}

function saveNow() {
  if (isLoadingChapter || (!isFlushingForSwitch && isProjectSwitching)) return Promise.resolve(false);
  if (!currentFile || (!dirty && saveRevision <= lastSavedRevision)) {
    return Promise.resolve(true);
  }
  clearTimeout(saveTimer);
  saveTimer = null;

  // Coda seriale stretta: massimo una singola richiesta HTTP in volo per volta
  const task = saveQueue.then(async () => {
    if (isLoadingChapter || (!isFlushingForSwitch && isProjectSwitching) || !currentFile || (!dirty && saveRevision <= lastSavedRevision)) {
      return true;
    }
    clearTimeout(saveTimer);
    saveTimer = null;

    const targetVault = currentFileVault || (state && state.vault) || "";
    const targetFile = currentFile;
    const targetContent = ta.value;
    const targetRev = saveRevision;

    // Se il vault del capitolo corrente non corrisponde al vault attivo nello stato,
    // rifiuta il salvataggio per evitare cross-vault corruption!
    if (currentFileVault && state && state.vault && currentFileVault !== state.vault) {
      return false;
    }

    setState(t("save_saving"), "busy");

    try {
      const res = await api("/api/save", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          file: targetFile,
          content: targetContent,
          base_version: fileVersion,
          vault: targetVault,
        }),
      });
      if (res && res.error) {
        throw new Error(res.error);
      }
      if (currentFile === targetFile && ((state && state.vault) || "") === targetVault) {
        fileVersion = res.version;
        if (targetRev > lastSavedRevision) {
          lastSavedRevision = targetRev;
        }
        if (saveRevision <= lastSavedRevision) {
          dirty = false;
          await notifyClientDirty();
          setState(t("save_saved") + fmtTime());
        } else {
          dirty = true;
          await notifyClientDirty();
          queueSave();
        }
      }
      return true;
    } catch (e) {
      setState(t("save_error") + ": " + e.message, "err");
      return false;
    }
  });

  saveQueue = task.catch(() => {});
  savePromise = task;
  return task;
}

ta.addEventListener("input", () => {
  if (isProjectSwitching || isBuilding || isLoadingChapter || ta.readOnly || !currentFile) return;
  renderPreview();
  queueSave();
  updateWordCount();
  markPublicationPreviewStale();
  if (isFocusMode) {
    pokeFocusHud();
    updateFocusHud();
    if (focusDimMode !== "off") {
      renderFocusDimming();
    }
    keepTypewriterCaretCentered();
  }
});
/* -------------------------------------------------- strumenti di scrittura */

function replaceRange(el, start, end, text, selectStart, selectEnd) {
  el.focus();
  let replaced = false;
  el.setSelectionRange(start, end);
  try {
    replaced = document.execCommand("insertText", false, text);
  } catch (_) {}
  if (!replaced) {
    el.setRangeText(text, start, end, "preserve");
  }
  if (typeof selectStart === "number" && typeof selectEnd === "number") {
    el.setSelectionRange(selectStart, selectEnd);
  }
  el.dispatchEvent(new Event("input", { bubbles: true }));
}

function getLineBounds(text, start, end) {
  const lineStart = text.lastIndexOf("\n", start - 1) + 1;
  let lineEnd = text.indexOf("\n", end);
  if (lineEnd === -1) lineEnd = text.length;
  return { lineStart, lineEnd };
}

function prefixCurrentLines(text, start, end, prefix) {
  const { lineStart, lineEnd } = getLineBounds(text, start, end);
  const chunk = text.substring(lineStart, lineEnd);
  const lines = chunk.split("\n");
  const allPrefixed = lines.length > 0 && lines.every(l => l.startsWith(prefix));
  const newLines = lines.map(l => {
    if (allPrefixed) {
      return l.substring(prefix.length);
    }
    const cleaned = l.replace(/^(#{2,4}\s+|>\s+|-\s+|\d+\.\s+)/, "");
    return prefix + cleaned;
  });
  return newLines.join("\n");
}

function prefixNumberLines(text, start, end) {
  const { lineStart, lineEnd } = getLineBounds(text, start, end);
  const chunk = text.substring(lineStart, lineEnd);
  const lines = chunk.split("\n");
  const allNumbered = lines.length > 0 && lines.every(l => /^\d+\.\s+/.test(l));
  const newLines = lines.map((l, i) => {
    if (allNumbered) {
      return l.replace(/^\d+\.\s+/, "");
    }
    const cleaned = l.replace(/^(#{2,4}\s+|>\s+|-\s+|\d+\.\s+)/, "");
    return `${i + 1}. ${cleaned}`;
  });
  return newLines.join("\n");
}

function applyFormatting(action) {
  if (isProjectSwitching || isBuilding || isLoadingChapter || ta.readOnly || !currentFile) return;

  const start = ta.selectionStart;
  const end = ta.selectionEnd;
  const val = ta.value;
  const selectedText = val.substring(start, end);

  let replacement = "";
  let newCursorStart = start;
  let newCursorEnd = end;

  switch (action) {
    case "bold":
      if (start >= 2 && end <= val.length - 2 && val.substring(start - 2, start) === "**" && val.substring(end, end + 2) === "**") {
        replaceRange(ta, start - 2, end + 2, selectedText, start - 2, end - 2);
        return;
      }
      if (selectedText.startsWith("**") && selectedText.endsWith("**") && selectedText.length >= 4) {
        replacement = selectedText.slice(2, -2);
        replaceRange(ta, start, end, replacement, start, start + replacement.length);
        return;
      }
      replacement = `**${selectedText || ""}**`;
      newCursorStart = selectedText ? start : start + 2;
      newCursorEnd = selectedText ? start + replacement.length : start + 2;
      replaceRange(ta, start, end, replacement, newCursorStart, newCursorEnd);
      return;

    case "italic":
      if (start >= 1 && end <= val.length - 1 && val[start - 1] === "*" && val[end] === "*" && (start < 2 || val[start - 2] !== "*") && (end >= val.length - 1 || val[end + 1] !== "*")) {
        replaceRange(ta, start - 1, end + 1, selectedText, start - 1, end - 1);
        return;
      }
      if (selectedText.startsWith("*") && selectedText.endsWith("*") && selectedText.length >= 2 && !selectedText.startsWith("**")) {
        replacement = selectedText.slice(1, -1);
        replaceRange(ta, start, end, replacement, start, start + replacement.length);
        return;
      }
      replacement = `*${selectedText || ""}*`;
      newCursorStart = selectedText ? start : start + 1;
      newCursorEnd = selectedText ? start + replacement.length : start + 1;
      replaceRange(ta, start, end, replacement, newCursorStart, newCursorEnd);
      return;

    case "h2":
      replacement = prefixCurrentLines(val, start, end, "## ");
      break;

    case "h3":
      replacement = prefixCurrentLines(val, start, end, "### ");
      break;

    case "quote":
      replacement = prefixCurrentLines(val, start, end, "> ");
      break;

    case "ul":
      replacement = prefixCurrentLines(val, start, end, "- ");
      break;

    case "ol":
      replacement = prefixNumberLines(val, start, end);
      break;

    case "break":
      const beforeChar = start > 0 ? val[start - 1] : "\n";
      const lead = (beforeChar === "\n") ? "" : "\n\n";
      replacement = lead + "***\n\n";
      newCursorStart = start + replacement.length;
      newCursorEnd = newCursorStart;
      break;

    case "link":
      if (selectedText) {
        replacement = `[${selectedText}](https://)`;
        newCursorStart = start + selectedText.length + 3;
        newCursorEnd = newCursorStart + 8;
      } else {
        replacement = `[testo](https://)`;
        newCursorStart = start + 1;
        newCursorEnd = start + 6;
      }
      break;
  }

  if (action === "h2" || action === "h3" || action === "quote" || action === "ul" || action === "ol") {
    const lineInfo = getLineBounds(val, start, end);
    replaceRange(ta, lineInfo.lineStart, lineInfo.lineEnd, replacement, lineInfo.lineStart, lineInfo.lineStart + replacement.length);
  } else {
    replaceRange(ta, start, end, replacement, newCursorStart, newCursorEnd);
  }
}

async function uploadAndInsertImage(file) {
  if (!file || !state || !state.vault) {
    showModal(t("modal_error"), "Apri prima una cartella libro per inserire immagini.", [[t("modal_close"), hideModal]]);
    return;
  }
  const curVault = state.vault;
  const targetFile = currentFile;
  const targetRevision = saveRevision;
  const start = ta.selectionStart;
  const end = ta.selectionEnd;
  const placeholder = ta.value.substring(start, end).trim() || "Didascalia";

  const formData = new FormData();
  formData.append("image", file, file.name || "immagine.png");

  try {
    const res = await fetch("/api/uploadimage", {
      method: "POST",
      headers: {
        "X-PubCraft-Vault": curVault,
      },
      body: formData,
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(err.error || res.statusText);
    }
    const data = await res.json();
    if (!data.filename) throw new Error("Caricamento fallito");

    // Verifica che né il vault né il capitolo siano cambiati durante l'upload asincrono
    if (!state || state.vault !== curVault) {
      console.warn("Upload completato ma vault cambiato: inserimento markdown omesso.");
      return;
    }
    if (currentFile !== targetFile) {
      console.warn("Upload completato ma capitolo cambiato durante l'upload: inserimento markdown omesso.");
      return;
    }

    const curStart = (ta.selectionStart !== undefined && saveRevision === targetRevision) ? start : ta.selectionStart;
    const curEnd = (ta.selectionEnd !== undefined && saveRevision === targetRevision) ? end : ta.selectionEnd;
    const md = `![${placeholder}](${data.rel_path || ("images/" + data.filename)})\n`;

    replaceRange(ta, curStart, curEnd, md, curStart + 2, curStart + 2 + placeholder.length);
  } catch (err) {
    showModal(t("modal_error"), "Impossibile inserire l'immagine: " + err.message, [[t("modal_close"), hideModal]]);
  }
}

// Inizializzazione bottoni toolbar scrittura
["tbBold", "tbItalic", "tbH2", "tbH3", "tbQuote", "tbUl", "tbOl", "tbBreak", "tbLink", "tbImage", "tbTable"].forEach(id => {
  $(id)?.addEventListener("mousedown", (e) => e.preventDefault());
});

$("tbBold")?.addEventListener("click", () => applyFormatting("bold"));
$("tbItalic")?.addEventListener("click", () => applyFormatting("italic"));
$("tbH2")?.addEventListener("click", () => applyFormatting("h2"));
$("tbH3")?.addEventListener("click", () => applyFormatting("h3"));
$("tbQuote")?.addEventListener("click", () => applyFormatting("quote"));
$("tbUl")?.addEventListener("click", () => applyFormatting("ul"));
$("tbOl")?.addEventListener("click", () => applyFormatting("ol"));
$("tbBreak")?.addEventListener("click", () => applyFormatting("break"));
$("tbLink")?.addEventListener("click", () => applyFormatting("link"));
$("tbImage")?.addEventListener("click", () => {
  if (isProjectSwitching || isBuilding || isLoadingChapter || ta.readOnly || !currentFile) return;
  $("tbImageInput")?.click();
});
$("tbImageInput")?.addEventListener("change", async (e) => {
  const file = e.target.files && e.target.files[0];
  if (file) {
    await uploadAndInsertImage(file);
    e.target.value = "";
  }
});

function openTableModal() {
  if (isProjectSwitching || isBuilding || isLoadingChapter || ta.readOnly || !currentFile) return;
  if ($("tableCols")) $("tableCols").value = "2";
  if ($("tableRows")) $("tableRows").value = "3";
  $("tableModal")?.classList.remove("hidden");
  setTimeout(() => {
    $("tableCols")?.focus();
    $("tableCols")?.select();
  }, 50);
}

function hideTableModal() {
  $("tableModal")?.classList.add("hidden");
  ta?.focus();
}

function insertMarkdownTable() {
  let cols = parseInt($("tableCols")?.value, 10);
  let rows = parseInt($("tableRows")?.value, 10);
  if (isNaN(cols) || cols < 1) cols = 2;
  if (cols > 10) cols = 10;
  if (isNaN(rows) || rows < 1) rows = 3;
  if (rows > 50) rows = 50;

  const colPrefix = t("table_col_prefix") || "Colonna";
  const cellPrefix = t("table_cell_prefix") || "Cella";

  let header = "|";
  let sep = "|";
  for (let c = 1; c <= cols; c++) {
    header += ` ${colPrefix} ${c} |`;
    sep += " --- |";
  }
  let md = `\n${header}\n${sep}\n`;
  for (let r = 1; r <= rows; r++) {
    let row = "|";
    for (let c = 1; c <= cols; c++) {
      row += ` ${cellPrefix} ${r}.${c} |`;
    }
    md += `${row}\n`;
  }
  md += "\n";

  hideTableModal();

  const start = ta.selectionStart !== undefined ? ta.selectionStart : ta.value.length;
  const end = ta.selectionEnd !== undefined ? ta.selectionEnd : start;
  const targetWord = `${colPrefix} 1`;
  const firstColIdx = md.indexOf(targetWord);
  const selStart = firstColIdx !== -1 ? start + firstColIdx : start;
  const selEnd = firstColIdx !== -1 ? selStart + targetWord.length : start;

  replaceRange(ta, start, end, md, selStart, selEnd);
}

$("tbTable")?.addEventListener("click", openTableModal);
$("btnTableCancel")?.addEventListener("click", hideTableModal);
$("btnTableInsert")?.addEventListener("click", insertMarkdownTable);
$("tableModal")?.addEventListener("click", (e) => {
  if (e.target === $("tableModal")) hideTableModal();
});
["tableCols", "tableRows"].forEach(id => {
  $(id)?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      insertMarkdownTable();
    } else if (e.key === "Escape") {
      e.preventDefault();
      hideTableModal();
    }
  });
});

// Incolla immagine da appunti
ta.addEventListener("paste", async (e) => {
  if (isProjectSwitching || isBuilding || isLoadingChapter || ta.readOnly || !currentFile) return;
  const items = e.clipboardData && e.clipboardData.items;
  if (!items) return;
  for (const item of items) {
    if (item.type.indexOf("image") !== -1) {
      e.preventDefault();
      const blob = item.getAsFile();
      if (blob) {
        await uploadAndInsertImage(blob);
      }
      return;
    }
  }
});

ta.addEventListener("keydown", (e) => {
  const isCmd = e.metaKey || e.ctrlKey;
  if (!isCmd) return;

  if (e.key === "s" || e.key === "S") {
    e.preventDefault();
    if (isProjectSwitching || isBuilding || isLoadingChapter || ta.readOnly || !currentFile) return;
    saveNow();
  } else if (e.key === "b" || e.key === "B") {
    e.preventDefault();
    applyFormatting("bold");
  } else if (e.key === "i" || e.key === "I") {
    e.preventDefault();
    applyFormatting("italic");
  } else if (e.key === "k" || e.key === "K") {
    e.preventDefault();
    applyFormatting("link");
  }
});

/* ----------------------------------------------------- anteprima (iframe) */

/* Base tipografica = replica di styles/book.css. Gli override dello stile
 * del libro (preset o style.css del vault) arrivano dopo e vincono. */
const PREVIEW_BASE = `
  @font-face {
    font-family: 'Great Vibes';
    font-style: normal;
    font-weight: 400;
    src: url('/static/fonts/GreatVibes-Regular.ttf') format('truetype');
  }
  @font-face {
    font-family: 'Cinzel Decorative';
    font-style: normal;
    font-weight: 400;
    src: url('/static/fonts/CinzelDecorative-Regular.ttf') format('truetype');
  }
  @font-face {
    font-family: 'Mrs Saint Delafield';
    font-style: normal;
    font-weight: 400;
    src: url('/static/fonts/MrsSaintDelafield-Regular.ttf') format('truetype');
  }
  @font-face {
    font-family: 'Cormorant';
    font-style: normal;
    font-weight: 600;
    src: url('/static/fonts/Cormorant-SemiBold.ttf') format('truetype');
  }
  @font-face {
    font-family: 'Cormorant SC';
    font-style: normal;
    font-weight: 600;
    src: url('/static/fonts/CormorantSC-SemiBold.ttf') format('truetype');
  }
  @font-face {
    font-family: 'Bodoni Moda';
    font-style: normal;
    font-weight: 500;
    src: url('/static/fonts/BodoniModa_18pt-Medium.ttf') format('truetype');
  }
  @font-face {
    font-family: 'Montserrat';
    font-style: normal;
    font-weight: 700;
    src: url('/static/fonts/Montserrat-Bold.ttf') format('truetype');
  }
  body { margin: 0; background: #fffdf8; }
  .preview {
    font-family: Palatino, "Palatino Linotype", "Book Antiqua", Georgia, serif; line-height: 1.7;
    max-width: 44em; margin: 0 auto; padding: 26px 32px 60px;
    color: #1a1815; background: #fffdf8;
  }
  .preview .chtitle {
    font-size: 1.45em; margin: .4em 0 1.6em; text-align: center;
    font-variant: small-caps; letter-spacing: .06em; font-weight: bold;
  }
  .preview .chtitle::after {
    content: "\\2766"; display: block; color: #8a6d3b; font-size: .8em; margin-top: .4em;
  }
  .preview h2 {
    font-size: 1.15em; margin: 1.8em 0 .5em; text-align: left; font-weight: bold;
  }
  .preview h3 { margin: 1.8em 0 .5em; font-size: 1.1em; }
  .preview p { text-align: justify; hyphens: auto; margin: .85em 0; text-indent: 1.5em; }
  .preview p.noindent, .preview .chtitle + p, .preview h2 + p, .preview h3 + p, .preview .first-para, .preview hr + p, .preview hr.sb + p { text-indent: 0; }
  .preview .dropcap { font-size: inherit; }
  .preview .first-phrase {
    font-variant: small-caps;
    font-feature-settings: "smcp", "c2sc";
    letter-spacing: .06em;
    font-weight: 500;
  }
  .preview blockquote { border-left: none; text-align: center; font-style: italic; margin: 1.6em 2em; }
  .preview blockquote::before { content: "\\201c"; display: block; font-size: 2em; color: #8a6d3b; }
  .preview img { max-width: 100%; height: auto; display: block; margin: 1.4em auto; }
  .preview code { font-family: "SF Mono", Menlo, monospace; font-size: .9em;
    background: #efe9dd; padding: 1px 5px; border-radius: 4px; }
`;

async function refreshStyle() {
  const id = state.book && state.book.style ? state.book.style : "classico";
  if (id === "classico") { styleCssText = ""; } else {
    try {
      styleCssText = await (await fetch("/api/stylecss?id=" + encodeURIComponent(id))).text();
    } catch { styleCssText = ""; }
  }
  const f = state.book && state.book.font ? state.book.font : "";
  if (!f) { bodyFontCssText = ""; } else {
    try {
      bodyFontCssText = await (await fetch("/api/fontcss?family=" + encodeURIComponent(f))).text();
    } catch { bodyFontCssText = ""; }
  }
  // ornamento di scena (default: asterischi)
  const sb = state.book && state.book.scenebreak ? state.book.scenebreak : "asterischi";
  try {
    sceneBreakCssText = await (await fetch("/api/scenebreakcss?id=" + encodeURIComponent(sb))).text();
  } catch { sceneBreakCssText = ""; }
}

let debPrev = null;
function renderPreview() {
  clearTimeout(debPrev);
  debPrev = setTimeout(() => {
    let inner = marked.parse(formatMarkdownForBook(ta.value.replace(/^---\n[\s\S]*?\n---\n/, "")));
    const curCh = (state.book && state.book.chapters) ? state.book.chapters.find((x) => x.file === currentFile) : null;
    const chType = (curCh && curCh.type) || "chapter";
    const isFront = (chType === "dedication" || chType === "epigraph");

    if (!isFront) {
      const op = (state.book && state.book.opening) || "dropcap";
      if (op === "smallcaps") {
        inner = inner.replace(/<p>([“"‘'«]*)([\p{L}\p{N}]+(?:\s+[\p{L}\p{N}]+){0,2})(.*?)<\/p>/u,
          '<p class="first-para">$1<span class="first-phrase">$2</span>$3</p>');
      } else if (op === "none") {
        inner = inner.replace(/<p>/, '<p class="first-para">');
      } else {
        inner = inner.replace(/<p>([“"‘'«]*)(\p{L})(.*?)<\/p>/u, (m, p1, p2, p3) => {
          let cls = "dropcap";
          const up = p2.toUpperCase();
          if (up === "I" || up === "Ì" || up === "Í" || up === "Î") cls = "dropcap dropcap-i";
          else if (up === "J") cls = "dropcap dropcap-j";
          return `<p class="first-para">${p1}<span class="${cls}">${p2}</span>${p3}</p>`;
        });
      }
    }

    let artClass = "preview";
    if (chType === "dedication") artClass += " preview-dedication";
    else if (chType === "epigraph") artClass += " preview-epigraph";
    else if (chType === "about-author") artClass += " preview-about-author";

    const title = (currentTitle && !isFront) ? '<h1 class="chtitle">' + esc(currentTitle) + '</h1>' : "";
    const darkPreviewStyle = isDarkMode ? `
      html, body {
        background: #141414 !important;
        color: #f0ede6 !important;
      }
      header {
        border-bottom-color: #333333 !important;
      }
      .preview {
        background: #141414 !important;
        color: #f0ede6 !important;
      }
      .preview h1, .preview h2, .preview h3, .preview .chtitle, .preview p, .preview blockquote, .preview .dropcap {
        color: #f0ede6 !important;
      }
      .preview code {
        background: #252525 !important;
        color: #f0ede6 !important;
      }
      .preview .byline {
        color: #9c978e !important;
      }
      .preview blockquote::before, .preview hr.orn, .preview hr.sb::after, .preview .chtitle::after {
        color: #d8a85a !important;
      }
    ` : "";
    prevFrame.srcdoc =
      '<!doctype html><html><head><base href="/"><style>' + PREVIEW_BASE + '</style>' +
      '<style>' + styleCssText + bodyFontCssText + sceneBreakCssText + darkPreviewStyle + '</style></head><body>' +
      '<article class="' + artClass + '">' + title + inner + '</article></body></html>';
  }, 150);
}

/* ----------------------------------------------------------------- modals */

function showModal(title, out, actions = []) {
  $("modalTitle").textContent = title;
  $("modalOut").textContent = out;
  const box = $("modalActions");
  box.innerHTML = "";
  if (!actions.length) actions = [[t("modal_close"), hideModal]];
  actions.forEach(action => {
    let label = "", fn = hideModal, cls = "";
    if (Array.isArray(action)) {
      [label, fn, cls] = action;
    } else if (action && typeof action === "object") {
      label = action.label || "";
      fn = action.onClick || hideModal;
      cls = action.danger ? "danger" : (action.cls || "");
    }
    const btn = document.createElement("button");
    btn.textContent = label;
    if (cls) btn.className = cls;
    btn.addEventListener("click", async () => {
      hideModal();
      if (fn) await fn();
    });
    box.appendChild(btn);
  });
  $("modal").classList.remove("hidden");
}
const hideModal = () => $("modal").classList.add("hidden");
$("modal").addEventListener("click", (e) => { if (e.target === $("modal")) hideModal(); });

async function runBuild(kind, params = {}) {
  if (isProjectSwitching || isBuilding || activeProjectMutations) return;
  if (!state.book) return;

  isBuilding = true;
  updateProjectLock();
  const buildVault = (state && state.vault) || "";
  const prevReadOnly = ta.readOnly;
  ta.readOnly = true;

  try {
    const saveOk = await flushSave();
    if (!saveOk) {
      showModal(t("save_failed"), t("save_failed_change_ch"), [
        [t("btn_retry_save"), async () => { hideModal(); await runBuild(kind, params); }],
        [t("btn_cancel"), hideModal],
      ]);
      return;
    }

    if (((state && state.vault) || "") !== buildVault) {
      showModal(t("modal_error"), "Il progetto è cambiato durante l'avvio della build.", [[t("modal_close"), hideModal]]);
      return;
    }

    showModal("Build in corso…", "Attendere, sto compilando con pubcraft…");
    const res = await api("/api/build", {
      method: "POST", body: JSON.stringify({ kind, ...params, vault: buildVault }),
    });
    const actions = [["Chiudi", hideModal]];
    if (res.path) {
      actions.unshift([isMac ? "Mostra nel Finder" : "Mostra nella cartella", () => {
        fetch("/api/reveal", {
          method: "POST", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: res.path }),
        });
      }, "cta"]);
    }
    if (res.ok) {
      setState(t("publish_ok") + " " + fmtTime());
      showModal("Fatto ✔", res.output || "", actions);
    } else {
      setState(t("build_error"), "err");
      showModal("Errore", (res.error || "") + "\n\n" + (res.output || ""), actions);
    }
  } catch (e) {
    setState(t("build_error"), "err");
    showModal("Errore", String(e), [["Chiudi", hideModal]]);
  } finally {
    isBuilding = false;
    updateProjectLock();
    if (currentFile && !isProjectSwitching && !isLoadingChapter) {
      ta.readOnly = false;
    } else {
      ta.readOnly = prevReadOnly;
    }
  }
}

/* ------------------------------ picker di file/cartelle (cross-platform) */

let fsState = null; // {mode:"dirs"|"files", exts, resolve, cur, sel, selectedFiles: Set, lastClickedIndex: number}

let fsLastItems = [];

function pickPath(mode, exts, title) {
  return new Promise((resolve) => {
    fsState = {
      mode,
      exts: exts || "",
      resolve,
      cur: "",
      sel: null,
      selectedFiles: new Set(),
      lastClickedIndex: -1,
    };
    $("fsTitle").textContent = title;
    $("btnFsNew").style.display = mode === "dirs" ? "" : "none";
    if ($("btnFsOpenFolder")) $("btnFsOpenFolder").style.display = "none";
    $("btnFsSelect").disabled = true;
    if ($("fsFilterInput")) $("fsFilterInput").value = "";
    $("fsModal").classList.remove("hidden");
    const startPath = workspaceDir || (state && state.workspaceDir) || null;
    fsBrowse(startPath);
  });
}

function renderFsList(items) {
  const ul = $("fsList");
  ul.innerHTML = "";
  const filter = ($("fsFilterInput")?.value || "").toLowerCase().trim();
  let filtered = items;
  if (filter) {
    filtered = items.filter(it => it.name.toLowerCase().includes(filter));
  }

  // Click sullo sfondo della lista per deselezionare
  ul.onclick = (e) => {
    if (e.target === ul) {
      if (fsState && fsState.selectedFiles) {
        fsState.selectedFiles.clear();
        fsState.sel = null;
        fsState.lastClickedIndex = -1;
        [...ul.children].forEach(c => c.classList.remove("sel"));
        updateSelectBtn();
      }
    }
  };

  if (filtered.length) {
    filtered.forEach((it, idx) => {
      const li = document.createElement("li");
      const ico = document.createElement("span");
      ico.textContent = it.dir ? "📁" : (isMediaFile(it.name) ? "🖼" : "📄");
      const n = document.createElement("span");
      n.className = "n"; n.textContent = it.name;
      li.append(ico, n);

      if (fsState && fsState.selectedFiles && fsState.selectedFiles.has(it.name)) {
        li.classList.add("sel");
      }

      if (it.dir) {
        li.addEventListener("click", () => fsBrowse(fsState.cur + "/" + it.name));
      } else {
        li.addEventListener("click", (e) => {
          e.stopPropagation();
          if (!fsState) return;
          const isToggle = e.metaKey || e.ctrlKey;
          const isRange = e.shiftKey && fsState.lastClickedIndex >= 0;

          if (isRange) {
            const start = Math.min(fsState.lastClickedIndex, idx);
            const end = Math.max(fsState.lastClickedIndex, idx);
            for (let i = start; i <= end; i++) {
              if (filtered[i] && !filtered[i].dir) {
                fsState.selectedFiles.add(filtered[i].name);
              }
            }
          } else if (isToggle) {
            if (fsState.selectedFiles.has(it.name)) {
              fsState.selectedFiles.delete(it.name);
            } else {
              fsState.selectedFiles.add(it.name);
            }
            fsState.lastClickedIndex = idx;
          } else {
            fsState.selectedFiles.clear();
            fsState.selectedFiles.add(it.name);
            fsState.lastClickedIndex = idx;
          }

          if (fsState.selectedFiles.size === 1) {
            fsState.sel = Array.from(fsState.selectedFiles)[0];
          } else {
            fsState.sel = null;
          }

          [...ul.children].forEach((child, cIdx) => {
            const itemObj = filtered[cIdx];
            if (itemObj && !itemObj.dir && fsState.selectedFiles.has(itemObj.name)) {
              child.classList.add("sel");
            } else {
              child.classList.remove("sel");
            }
          });

          updateSelectBtn();
        });

        li.addEventListener("dblclick", (e) => {
          e.stopPropagation();
          if (!fsState) return;
          fsState.selectedFiles.clear();
          fsState.selectedFiles.add(it.name);
          fsState.sel = it.name;
          fsSelect();
        });
      }
      ul.appendChild(li);
    });
  } else {
    const li = document.createElement("li");
    li.innerHTML = '<i class="muted">' + esc(t("empty_folder")) + '</i>';
    ul.appendChild(li);
  }
}

async function fsBrowse(p) {
  let url = "/api/fs?mode=" + fsState.mode +
    (fsState.exts ? "&exts=" + encodeURIComponent(fsState.exts) : "") +
    (p ? "&path=" + encodeURIComponent(p) : "");
  const res = await fetch(url).then((r) => r.json());
  if (res.error) { alert(res.error); return; }
  fsState.cur = res.path;
  fsState.sel = null;
  fsState.selectedFiles = new Set();
  fsState.lastClickedIndex = -1;
  if (res.workspace) workspaceDir = res.workspace;
  updateSelectBtn();
  // breadcrumb cliccabile
  const cr = $("fsBreadcrumb");
  cr.innerHTML = "";
  const parts = res.path.split("/").filter(Boolean);
  if (!parts.length) {
    cr.appendChild(Object.assign(document.createElement("b"), { textContent: "/" }));
  }
  parts.forEach((p2, i) => {
    if (i) cr.appendChild(Object.assign(document.createElement("span"), { textContent: " / " }));
    const path = "/" + parts.slice(0, i + 1).join("/");
    if (i === parts.length - 1) {
      cr.appendChild(Object.assign(document.createElement("b"), { textContent: p2 }));
    } else {
      const b = document.createElement("button");
      b.textContent = p2;
      b.addEventListener("click", () => fsBrowse(path));
      cr.appendChild(b);
    }
  });
  $("btnFsUp").disabled = !res.parent;
  fsLastItems = res.items || [];
  renderFsList(fsLastItems);
}

function isMediaFile(name) {
  return /\.(jpe?g|png|gif|webp)$/i.test(name);
}

function updateSelectBtn() {
  if (!fsState) return;
  const hasCur = Boolean(fsState.cur && fsState.cur !== "/" && fsState.cur !== ".");
  const count = fsState.selectedFiles ? fsState.selectedFiles.size : 0;

  if (count > 1) {
    $("btnFsSelect").disabled = false;
    $("btnFsSelect").textContent = t("fs_select_files_count", { n: count }) || `Scegli ${count} file`;
  } else if (count === 1) {
    $("btnFsSelect").disabled = false;
    $("btnFsSelect").textContent = t("fs_select_file") || t("fs_select");
  } else if (fsState.mode === "dirs") {
    $("btnFsSelect").disabled = !hasCur;
    $("btnFsSelect").textContent = t("fs_select_folder") || t("fs_select");
  } else {
    // In modalità "files", se nessun file specifico è selezionato ma siamo dentro una cartella,
    // permettiamo di scegliere l'intera cartella (per aprire tutti i capitoli contemporaneamente).
    $("btnFsSelect").disabled = !hasCur;
    $("btnFsSelect").textContent = hasCur ? (t("fs_open_folder") || "📁 Apri questa cartella") : t("fs_select");
  }
  if ($("btnFsOpenFolder")) {
    $("btnFsOpenFolder").style.display = hasCur ? "" : "none";
  }
}

async function fsSelect() {
  if (!fsState) return;
  const count = fsState.selectedFiles ? fsState.selectedFiles.size : 0;
  let val;
  if (fsState.mode === "dirs") {
    val = fsState.cur;
  } else if (count > 1) {
    const list = Array.from(fsState.selectedFiles);
    val = {
      dir: fsState.cur,
      files: list,
      path: fsState.cur + "/" + list[0],
      toString: () => fsState.cur,
    };
  } else if (count === 1) {
    const name = Array.from(fsState.selectedFiles)[0];
    val = {
      dir: fsState.cur,
      files: [name],
      path: fsState.cur + "/" + name,
      toString: () => fsState.cur + "/" + name,
    };
  } else {
    val = {
      dir: fsState.cur,
      files: [],
      path: fsState.cur,
      toString: () => fsState.cur,
    };
  }

  if (!val) return;
  const resolve = fsState.resolve;
  fsState = null;
  $("fsModal").classList.add("hidden");
  resolve(val);
}

function fsOpenCurrentFolder() {
  if (!fsState || !fsState.cur) return;
  const val = fsState.cur;
  const resolve = fsState.resolve;
  fsState = null;
  $("fsModal").classList.add("hidden");
  resolve(val);
}
const fsCancel = () => {
  if (!fsState) return;
  const resolve = fsState.resolve;
  fsState = null;
  $("fsModal").classList.add("hidden");
  resolve(null);
};

async function newFolderInCurrent() {
  if (!fsState) return;
  const name = prompt(t("fs_new_folder_prompt"));
  if (!name) return;
  const res = await fetch("/api/mkdir", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path: fsState.cur, name }),
  }).then((r) => r.json());
  if (res.error) { alert(res.error); return; }
  fsBrowse(res.path);
}

const chooseDir = (prompt) => pickPath("dirs", "", prompt);

/* ---------------------------------------------------------------- actions */

const openModal = () => $("openModal").classList.remove("hidden");
const hideOpenModal = () => $("openModal").classList.add("hidden");

function slugProjectFolder(value) {
  const slug = String(value).normalize("NFD").replace(/[\u0300-\u036f]/g, "")
    .toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  return slug || "mio-libro";
}

function showNewProjectModal() {
  if (isProjectSwitching || isBuilding || activeProjectMutations) return;
  hideOpenModal();
  hideModal();
  autoProjectFolder = true;
  $("npTitle").value = "";
  $("npAuthor").value = "";
  $("npParentDir").value = defaultParentDir;
  $("npFolderName").value = "";
  $("npStyle").value = "classic";
  $("newProjectModal").classList.remove("hidden");
  $("npTitle").focus();
}

async function runProjectTransition(actionFn) {
  if (isProjectSwitching || isBuilding) return false;
  let waitMut = 0;
  while (activeProjectMutations > 0 && waitMut < 50) {
    await new Promise(r => setTimeout(r, 20));
    waitMut++;
  }
  if (activeProjectMutations > 0) return false;
  isProjectSwitching = true;
  updateProjectLock();
  const transitionId = ++projectTransitionId;
  const prevReadOnly = ta.readOnly;
  ta.readOnly = true;

  try {
    isFlushingForSwitch = true;
    let chOk = false;
    let noteOk = false;
    try {
      chOk = await flushSave();
      noteOk = await flushNoteSave();
      await notifyClientDirty();
    } finally {
      isFlushingForSwitch = false;
    }

    if (!chOk || !noteOk) {
      showModal(t("save_failed"), t("save_failed_change_ch"), [
        [t("btn_retry_save"), async () => { hideModal(); await runProjectTransition(actionFn); }],
        [t("btn_cancel"), hideModal],
      ]);
      return false;
    }

    const newState = await actionFn();
    if (transitionId !== projectTransitionId) return false;
    if (!newState) return false;

    chapterNavId++;
    state = newState;
    if (state.vault && state.book) {
      await afterOpenBook();
    } else {
      updateDocVisibility();
    }
    return true;
  } catch (err) {
    if (transitionId === projectTransitionId) {
      showModal(t("modal_error"), String(err), [[t("modal_close"), hideModal]]);
    }
    return false;
  } finally {
    if (transitionId === projectTransitionId) {
      isProjectSwitching = false;
      updateProjectLock();
      if (currentFile && !isLoadingChapter) {
        ta.readOnly = false;
      } else {
        ta.readOnly = true;
      }
    }
  }
}

async function createNewProject() {
  if (isProjectSwitching || isBuilding) return;
  const title = $("npTitle").value.trim();
  const parentDir = $("npParentDir").value.trim();
  if (!title) { alert(t("enter_book_title")); return; }
  if (!parentDir) { alert(t("select_project_folder")); return; }
  const request = {
    title,
    author: $("npAuthor").value.trim(),
    parentDir,
    folder: $("npFolderName").value.trim() || slugProjectFolder(title),
    style: $("npStyle").value || "classic",
    opening: "dropcap",
    language: currentLang,
  };
  $("btnNpCreate").disabled = true;
  try {
    await runProjectTransition(async () => {
      const res = await api("/api/newproject", { method: "POST", body: JSON.stringify(request) });
      if (res.error) throw new Error(res.error);
      $("newProjectModal").classList.add("hidden");
      return res;
    });
  } finally {
    $("btnNpCreate").disabled = false;
  }
}

async function importDocx() {
  if (isProjectSwitching || isBuilding) return;
  const sel = await pickPath("files", ".docx,.vellum,.md,.markdown,.odt,.pdf", t("pick_docx_title"));
  if (!sel) return;
  const targetDir = typeof sel === "object" ? sel.dir : sel;
  const targetFiles = (typeof sel === "object" && sel.files && sel.files.length) ? sel.files : null;
  const singleFile = (typeof sel === "object" && sel.path) ? sel.path : (typeof sel === "string" ? sel : null);
  const display = targetFiles && targetFiles.length > 1
    ? `${targetDir} (${targetFiles.length} file)`
    : (singleFile || targetDir);

  showModal(t("importing_doc"), t("importing_wait", { dir: display }));
  try {
    await runProjectTransition(async () => {
      const payload = targetFiles && targetFiles.length > 0
        ? { dir: targetDir, files: targetFiles }
        : { dir: targetDir, file: singleFile || targetDir };
      const res = await api("/api/import-manuscript", {
        method: "POST", body: JSON.stringify(payload),
      });
      hideModal();
      if (res.error) throw new Error(res.error);
      return res;
    });
  } catch (e) {
    hideModal();
    showModal(t("import_failed"), String(e), [[t("modal_close"), hideModal]]);
  }
}

async function afterOpenBook() {
  clearTimeout(saveTimer);
  saveTimer = null;
  currentFile = "";
  currentTitle = "";
  currentFileVault = (state && state.vault) || "";
  fileVersion = "";
  dirty = false;
  saveRevision = 0;
  lastSavedRevision = 0;
  saveQueue = Promise.resolve();
  savePromise = null;
  customPrintDir = "";
  printConfigOutDir = "";
  ta.value = "";
  ta.readOnly = true;
  currentNoteFile = "";
  resetNoteEditor();
  await loadNotes();
  updatePrintDestinationLabel();
  resetPublicationPreview();
  await refreshStyle();
  renderBook();
  updateDocVisibility();
  loadCovers();
  initStyleMenu();
  initOpeningMenu();
  if (state.book && state.book.chapters && state.book.chapters.length) {
    await openChapter(state.book.chapters[0].file, true);
    setView("split");
  } else {
    updateDocVisibility();
  }
}

async function openVaultFolder() {
  if (isProjectSwitching || isBuilding) return;
  const sel = await pickPath("dirs", "", t("pick_vault_title"));
  if (!sel) return;
  const targetPath = typeof sel === "string" ? sel : (sel.path || sel.dir);
  const targetFiles = (typeof sel === "object" && sel.files && sel.files.length) ? sel.files : undefined;
  await runProjectTransition(async () => {
    const res = await api("/api/openat", {
      method: "POST", body: JSON.stringify({ path: targetPath, files: targetFiles }),
    });
    if (res.error) throw new Error(res.error);
    return res;
  });
}

async function importArchive() {
  if (isProjectSwitching || isBuilding) return;
  const sel = await pickPath("files", ".docx,.vellum,.md,.markdown,.odt,.pdf", t("pick_docx_title"));
  if (!sel) return;
  const targetDir = typeof sel === "object" ? sel.dir : sel;
  const targetFiles = (typeof sel === "object" && sel.files && sel.files.length) ? sel.files : null;
  const singleFile = (typeof sel === "object" && sel.path) ? sel.path : (typeof sel === "string" ? sel : null);
  const display = targetFiles && targetFiles.length > 1
    ? `${targetDir} (${targetFiles.length} file)`
    : (singleFile || targetDir);

  showModal(t("importing_book"), t("importing_book_wait", { dir: display }));
  try {
    await runProjectTransition(async () => {
      const payload = targetFiles && targetFiles.length > 0
        ? { dir: targetDir, files: targetFiles }
        : { dir: targetDir, file: singleFile || targetDir };
      const res = await api("/api/import-manuscript", {
        method: "POST", body: JSON.stringify(payload),
      });
      hideModal();
      if (res.error) throw new Error(res.error);
      return res;
    });
  } catch (e) {
    hideModal();
    showModal(t("import_failed"), String(e), [[t("modal_close"), hideModal]]);
  }
}

function newChapter() {
  if (isProjectSwitching || isBuilding || !state.book) return;
  $("newChapterType").value = "chapter";
  $("newChapterTitle").value = "";
  $("newChapterModal").classList.remove("hidden");
  $("newChapterTitle").focus();
}

function hideNewChapterModal() {
  $("newChapterModal").classList.add("hidden");
}

async function deleteChapter(c) {
  if (isProjectSwitching || isBuilding) return;
  if (!confirm(t("confirm_delete_chapter", { title: c.title }))) return;
  try {
    const res = await api("/api/deletechapter", {
      method: "POST", body: JSON.stringify({ file: c.file }),
    });
    state.book = res.book;
    if (currentFile === c.file) {
      currentFile = "";
      renderBook();
      updateDocVisibility();
      if (state.book.chapters.length) await openChapter(state.book.chapters[0].file);
    } else {
      renderBook();
    }
  } catch (e) {
    showModal("Errore", String(e), [["Chiudi", hideModal]]);
  }
}

async function kindleBuild() {
  if (!state.book) return;
  const dir = await chooseDir("Cartella dove salvare l'EPUB");
  if (!dir) return;
  runBuild("kindle", { outdir: dir });
}

/* ------------------------------------------------------ modal PDF stampa */

function isSafeURL(url, isImg = false) {
  if (!url) return false;
  const trimmed = url.trim();
  if (/^javascript:/i.test(trimmed) || /^data:(?!image\/)/i.test(trimmed)) return false;
  return true;
}

function sanitizeHTML(html) {
  if (!html) return "";
  try {
    const parser = new DOMParser();
    const doc = parser.parseFromString(html, "text/html");
    const badTags = ["script", "iframe", "object", "embed", "link", "form", "meta", "style", "base", "applet", "frame", "frameset"];
    for (const tag of badTags) {
      doc.querySelectorAll(tag).forEach((el) => el.remove());
    }
    doc.querySelectorAll("*").forEach((el) => {
      const attrs = Array.from(el.attributes);
      for (const attr of attrs) {
        const name = attr.name.toLowerCase();
        if (name.startsWith("on") || name === "formaction" || name === "action" || name === "srcdoc") {
          el.removeAttribute(attr.name);
          continue;
        }
        if (name === "href" || name === "xlink:href") {
          if (!isSafeURL(attr.value, false)) el.removeAttribute(attr.name);
        } else if (name === "src") {
          if (el.tagName.toLowerCase() !== "img" || !isSafeURL(attr.value, true)) {
            el.removeAttribute(attr.name);
          }
        }
      }
    });
    return doc.body.innerHTML;
  } catch (e) {
    return html.replace(/<[^>]*>/g, "");
  }
}

async function initPrintModal() {
  try {
    const cfg = await api("/api/printconfig");
    printConfigOutDir = cfg.outdir || "";
    customPrintDir = cfg.outdir && cfg.outdir !== cfg.default_outdir ? cfg.outdir : "";
    if (cfg.trim && [...$("trim").options].some(option => option.value === cfg.trim)) $("trim").value = cfg.trim;
    if (cfg.pages > 0) $("pages").value = cfg.pages;
    $("bleed").checked = !!cfg.bleed;
  } catch {
    printConfigOutDir = "";
    customPrintDir = "";
  }
  updatePrintDestinationLabel();

  const pChSel = $("printPreviewChapter");
  if (pChSel && state.book && state.book.chapters) {
    pChSel.innerHTML = "";
    state.book.chapters.forEach((ch) => {
      const opt = document.createElement("option");
      opt.value = ch.file;
      opt.textContent = ch.title ? `${ch.title} (${ch.file})` : ch.file;
      pChSel.appendChild(opt);
    });
    if (currentFile && state.book.chapters.some((c) => c.file === currentFile)) {
      pChSel.value = currentFile;
    } else if (state.book.chapters.length > 0) {
      pChSel.value = state.book.chapters[0].file;
    }
    if (!pChSel.dataset.bound) {
      pChSel.dataset.bound = "1";
      pChSel.addEventListener("change", () => {
        updateStyleSample();
      });
    }
  }

  const btnViewPrint = $("btnOpenViewPrint");
  if (btnViewPrint) {
    if (!btnViewPrint.dataset.bound) {
      btnViewPrint.dataset.bound = "1";
      btnViewPrint.addEventListener("click", () => {
        window.open("/api/viewprint/", "_blank");
      });
    }
    try {
      const testVp = await fetch("/api/viewprint/", { method: "HEAD" });
      btnViewPrint.style.display = testVp.ok ? "inline-block" : "none";
    } catch {
      btnViewPrint.style.display = "none";
    }
  }

  const sel = $("bookStyle");
  if (sel) {
    if (!sel.options.length) {
      try {
        const { styles } = await api("/api/styles");
        styles.forEach((s) => sel.appendChild(new Option(s.name, s.id)));
      } catch { /* resta vuoto */ }
      sel.addEventListener("change", async () => {
        try {
          const res = await api("/api/setstyle", {
            method: "POST", body: JSON.stringify({ style: sel.value }),
          });
          state.book = res.book;
          await refreshStyle();
          renderPreview();
          initStyleMenu();
        } catch (e) { showModal("Errore", String(e), [["Chiudi", hideModal]]); }
        updateStyleSample();
      });
    }
    sel.value = (state.book && state.book.style) || "classico";
  }

  const opSel = $("bookOpening");
  if (opSel) {
    opSel.value = (state.book && state.book.opening) ? state.book.opening : "dropcap";
    if (!opSel.dataset.bound) {
      opSel.dataset.bound = "1";
      opSel.addEventListener("change", async () => {
        await selectOpening(opSel.value);
        updateStyleSample();
      });
    }
  }

  const fsel = $("bodyFont");
  if (fsel) {
    if (!fsel.options.length) {
      try {
        const { fonts } = await api("/api/bodyfonts");
        fonts.forEach((f) => fsel.appendChild(new Option(f.name, f.id)));
      } catch { /* resta vuoto */ }
      if (!fsel.dataset.bound) {
        fsel.dataset.bound = "1";
        fsel.addEventListener("change", async () => {
          try {
            const res = await api("/api/setfont", {
              method: "POST", body: JSON.stringify({ font: fsel.value }),
            });
            state.book = res.book;
            await refreshStyle();
            renderPreview();
          } catch (e) { showModal("Errore", String(e), [["Chiudi", hideModal]]); }
          updateStyleSample();
        });
      }
    }
    fsel.value = (state.book && state.book.font) || "";
  }

  const sbSel = $("sceneBreak");
  if (sbSel) {
    if (!sbSel.options.length) {
      sbSel.appendChild(new Option(t("sb_asterisks"), "asterischi"));
      sbSel.appendChild(new Option(t("sb_fleuron"), "fleuron"));
      sbSel.appendChild(new Option(t("sb_lozenge"), "losanga"));
      sbSel.appendChild(new Option(t("sb_line"), "linea"));
      if (!sbSel.dataset.bound) {
        sbSel.dataset.bound = "1";
        sbSel.addEventListener("change", async () => {
          try {
            const res = await api("/api/setscenebreak", {
              method: "POST", body: JSON.stringify({ scenebreak: sbSel.value }),
            });
            state.book = res.book;
            await refreshStyle();
            renderPreview();
          } catch (e) { showModal("Errore", String(e), [["Chiudi", hideModal]]); }
          updateStyleSample();
        });
      }
    }
    sbSel.value = (state.book && state.book.scenebreak) || "asterischi";
  }

  await refreshStyle();
  await updateStyleSample();
  // stima pagine automatica dai parole totali
  try {
    const st = await api("/api/stats");
    $("pages").placeholder = t("pages_est_ph", {
      pages: st.pages_est,
      words: st.words.toLocaleString(currentLang),
    });
    if (!$("pages").value) $("pages").value = st.pages_est;
  } catch { /* niente */ }
}

async function updateStyleSample() {
  const requestId = ++styleSampleRequestId;
  const sel = $("bookStyle"), fsel = $("bodyFont"), opSel = $("bookOpening");
  let css = "";
  if (sel && sel.value && sel.value !== "classico") {
    try {
      css += await (await fetch("/api/stylecss?id=" + encodeURIComponent(sel.value))).text();
    } catch { css = ""; }
  }
  if (fsel && fsel.value) {
    try {
      css += await (await fetch("/api/fontcss?family=" + encodeURIComponent(fsel.value))).text();
    } catch { /* niente */ }
  }
  const sb = (state.book && state.book.scenebreak) || ($("sceneBreak") && $("sceneBreak").value) || "asterischi";
  try {
    css += await (await fetch("/api/scenebreakcss?id=" + encodeURIComponent(sb))).text();
  } catch { /* niente */ }

  const pChSel = $("printPreviewChapter");
  const targetFile = (pChSel && pChSel.value) || currentFile || (state.book && state.book.chapters && state.book.chapters[0] ? state.book.chapters[0].file : "");

  let rawContent = "";
  if (targetFile && targetFile === currentFile && ta) {
    rawContent = ta.value;
  } else if (targetFile) {
    try {
      const res = await api("/api/chapter?file=" + encodeURIComponent(targetFile));
      rawContent = res.content || "";
    } catch {
      rawContent = "";
    }
  }

  const curCh = (state.book && state.book.chapters) ? state.book.chapters.find((x) => x.file === targetFile) : null;
  const chType = (curCh && curCh.type) || "chapter";
  const isFront = (chType === "dedication" || chType === "epigraph");
  const chTitle = curCh ? (curCh.title || targetFile) : "";

  let clean = rawContent.replace(/^---\n[\s\S]*?\n---\n/, "");
  let inner = "";
  if (clean.trim()) {
    inner = marked.parse(formatMarkdownForBook(clean));
  } else {
    inner = '<p style="color:var(--muted);font-style:italic;">' + esc(t("preview_empty")) + '</p>';
  }

  inner = sanitizeHTML(inner);

  const op = (opSel && opSel.value) || (state.book && state.book.opening) || "dropcap";
  if (!isFront) {
    if (op === "smallcaps") {
      inner = inner.replace(/<p>([“"‘'«]*)([\p{L}\p{N}]+(?:\s+[\p{L}\p{N}]+){0,2})(.*?)<\/p>/u,
        '<p class="first-para">$1<span class="first-phrase">$2</span>$3</p>');
    } else if (op === "none") {
      inner = inner.replace(/<p>/, '<p class="first-para">');
    } else {
      inner = inner.replace(/<p>([“"‘'«]*)(\p{L})(.*?)<\/p>/u, (m, p1, p2, p3) => {
        let cls = "dropcap";
        const up = p2.toUpperCase();
        if (up === "I" || up === "Ì" || up === "Í" || up === "Î") cls = "dropcap dropcap-i";
        else if (up === "J") cls = "dropcap dropcap-j";
        return `<p class="first-para">${p1}<span class="${cls}">${p2}</span>${p3}</p>`;
      });
    }
  }

  let artClass = "preview";
  if (chType === "dedication") artClass += " preview-dedication";
  else if (chType === "epigraph") artClass += " preview-epigraph";
  else if (chType === "about-author") artClass += " preview-about-author";

  const hasH1 = /<h[12][^>]*>/i.test(inner);
  const titleHtml = (!hasH1 && chTitle && !isFront) ? '<h1 class="chtitle">' + esc(chTitle) + '</h1>' : "";

  if (requestId !== styleSampleRequestId) return;
  const darkSampleStyle = isDarkMode ? "html, body { background: #181818 !important; color: #f0ede6 !important; } .preview { background: #181818 !important; color: #f0ede6 !important; } h1, h2, h3, .chtitle, p, blockquote, .dropcap { color: #f0ede6 !important; }" : "";
  $("styleSample").srcdoc =
    '<!doctype html><html><head><base href="/"><style>' + PREVIEW_BASE + '</style>' +
    '<style>' + css + darkSampleStyle + '</style></head><body>' +
    '<article class="' + artClass + '">' + titleHtml + inner + '</article></body></html>';
}

async function pickPrintDir() {
  const dir = await chooseDir(t("pick_print_dir_title"));
  if (!dir) return;
  customPrintDir = dir;
  printConfigOutDir = dir;
  updatePrintDestinationLabel();
}

async function printModalGo() {
  const trim = $("trim").value;
  const pages = parseInt($("pages").value, 10) || 0;
  const bleed = $("bleed").checked;
  $("printModal").classList.add("hidden");
  runBuild("print", { trim, pages, bleed, outdir: printConfigOutDir });
}

/* ------------------------------------------------------------ menu stili */

let styleNames = {};

function styleLabel() {
  const cur = state.book ? (state.book.style || "classico") : "classico";
  $("btnStyle").textContent = t("btn_style") + (styleNames[cur] || cur) + " ▾";
}

async function initStyleMenu() {
  const list = $("styleList");
  list.innerHTML = "";
  try {
    const { styles } = await api("/api/styles");
    styleNames = {};
    styles.forEach((s) => { styleNames[s.id] = s.name; });
    const cur = state.book ? (state.book.style || "classico") : "classico";
    styles.forEach((s) => {
      const b = document.createElement("button");
      b.className = "item";
      b.innerHTML = esc(s.name) + (s.id === cur ? ' <span class="check">✓</span>' : "");
      b.addEventListener("click", () => { hideStyleMenu(); selectStyle(s.id); });
      list.appendChild(b);
    });
  } catch { /* niente */ }
  styleLabel();
}

const hideStyleMenu = () => $("styleList").classList.add("hidden");

async function selectStyle(id) {
  try {
    const res = await api("/api/setstyle", {
      method: "POST", body: JSON.stringify({ style: id }),
    });
    state.book = res.book;
    await refreshStyle();
    renderPreview();
    initStyleMenu();
  } catch (e) {
    showModal("Errore", String(e), [["Chiudi", hideModal]]);
  }
}

/* -------------------------------------------------- menu inizio capitolo */

function getOpeningOptions() {
  return [
    { id: "dropcap", name: t("opening_dropcap") },
    { id: "smallcaps", name: t("opening_smallcaps") },
    { id: "none", name: t("opening_none") },
  ];
}

function openingLabel() {
  const cur = (state.book && state.book.opening) ? state.book.opening : "dropcap";
  const item = getOpeningOptions().find((o) => o.id === cur);
  $("btnOpening").textContent = t("btn_opening") + (item ? item.name : t("opening_dropcap")) + " ▾";
}

function initOpeningMenu() {
  const list = $("openingList");
  list.innerHTML = "";
  const cur = (state.book && state.book.opening) ? state.book.opening : "dropcap";
  getOpeningOptions().forEach((o) => {
    const b = document.createElement("button");
    b.className = "item";
    b.innerHTML = esc(o.name) + (o.id === cur ? ' <span class="check">✓</span>' : "");
    b.addEventListener("click", () => { hideOpeningMenu(); selectOpening(o.id); });
    list.appendChild(b);
  });
  openingLabel();
}

const hideOpeningMenu = () => $("openingList").classList.add("hidden");

async function selectOpening(id) {
  try {
    const res = await api("/api/setopening", {
      method: "POST", body: JSON.stringify({ opening: id }),
    });
    state.book = res.book;
    renderPreview();
    initOpeningMenu();
    if ($("bookOpening")) $("bookOpening").value = id;
    if (!$("printModal").classList.contains("hidden")) updateStyleSample();
  } catch (e) {
    showModal("Errore", String(e), [["Chiudi", hideModal]]);
  }
}

/* ------------------------------------------------------------- copertine */

let covers = [];
let openThumb = null;

async function loadCovers() {
  try {
    const res = await api("/api/covers");
    covers = res.covers;
  } catch { covers = []; }
  renderCovers();
}

function renderCovers() {
  const ul = $("coverList");
  ul.innerHTML = "";
  covers.forEach((c) => {
    const li = document.createElement("li");
    li.dataset.name = c.name;
    const icon = document.createElement("span");
    icon.className = "icon";
    icon.textContent = c.kind === "image" ? "🖼" : c.kind === "other" ? "📄" : "📕";
    const t = document.createElement("span");
    t.className = "t";
    t.textContent = c.name;
    const x = document.createElement("button");
    x.className = "x"; x.textContent = "✕"; x.title = "Sposta in .cestina";
    x.addEventListener("click", (e) => { e.stopPropagation(); deleteCover(c); });
    li.append(icon, t, x);
    li.addEventListener("click", () => toggleThumb(c));
    ul.appendChild(li);
    if (openThumb === c.name) ul.appendChild(thumbRow(c));
  });
}

function toggleThumb(c) {
  openThumb = openThumb === c.name ? null : c.name;
  renderCovers();
}

function thumbRow(c) {
  const row = document.createElement("div");
  row.className = "thumbRow";
  // bottoni
  const actions = document.createElement("div");
  actions.className = "rowactions";
  const btnOpen = document.createElement("button");
  btnOpen.textContent = "Apri";
  btnOpen.addEventListener("click", (e) => {
    e.stopPropagation();
    fetch("/api/opencover", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: c.name }),
    });
  });
  actions.appendChild(btnOpen);
  if (c.kind === "image") {
    const btnEpub = document.createElement("button");
    btnEpub.textContent = "→ EPUB";
    btnEpub.title = "La rende la copertina dell'EPUB Kindle";
    btnEpub.addEventListener("click", (e) => { e.stopPropagation(); useAsEpubCover(c.name); });
    actions.appendChild(btnEpub);
  }
  row.appendChild(actions);
  // anteprima
  const na = (msg) => {
    const d = document.createElement("div");
    d.className = "thumbNA";
    d.textContent = msg;
    row.prepend(d);
  };
  if (c.kind === "image") {
    const img = document.createElement("img");
    img.alt = c.name;
    img.onerror = () => { img.remove(); na("Anteprima non disponibile"); };
    img.src = "/api/covermedia?file=" + encodeURIComponent(c.name);
    row.prepend(img);
  } else if (c.kind === "pdf" && window.pdfjsLib) {
    // PDF: miniatura renderizzata nel browser (pdf.js) — cross-platform
    renderPdfThumb(c, row, actions);
  } else if (c.kind === "pdf" || c.kind === "thumb") {
    // fallback server-side (qlmanage su macOS)
    const img = document.createElement("img");
    img.alt = c.name;
    img.onerror = () => { img.remove(); na("Anteprima non disponibile — usa “Apri”"); };
    img.src = "/api/covermedia?file=" + encodeURIComponent(c.name) + "&thumb=1";
    row.prepend(img);
  } else {
    na("Nessuna anteprima per questo formato");
  }
  return row;
}

async function renderPdfThumb(c, row, actions) {
  try {
    const buf = await (await fetch("/api/covermedia?file=" + encodeURIComponent(c.name))).arrayBuffer();
    const doc = await pdfjsLib.getDocument({ data: buf }).promise;
    const page = await doc.getPage(1);
    const base = page.getViewport({ scale: 1 });
    const vp = page.getViewport({ scale: 190 / base.width });
    const canvas = document.createElement("canvas");
    canvas.width = vp.width; canvas.height = vp.height;
    await page.render({ canvasContext: canvas.getContext("2d"), viewport: vp }).promise;
    row.prepend(canvas);
  } catch {
    const d = document.createElement("div");
    d.className = "thumbNA";
    d.textContent = "Anteprima non disponibile — usa “Apri”";
    row.prepend(d);
  }
}

async function deleteCover(c) {
  if (!confirm(t("move_to_trash_confirm", { name: c.name }))) return;
  try {
    const res = await api("/api/delcover", {
      method: "POST", body: JSON.stringify({ name: c.name }),
    });
    covers = res.covers;
    openThumb = null;
    renderCovers();
  } catch (e) {
    showModal("Errore", String(e), [["Chiudi", hideModal]]);
  }
}

async function addCover() {
  if (!state.vault) return;
  try {
    const sel = await pickPath("files",
      ".jpg,.jpeg,.png,.gif,.webp,.pdf,.tif,.tiff,.psd,.ai,.heic",
      t("pick_cover_title"));
    if (!sel) return;
    const targetFile = typeof sel === "string" ? sel : (sel.path || (sel.dir && sel.files && sel.files[0] ? sel.dir + "/" + sel.files[0] : null));
    if (!targetFile) return;
    const res = await api("/api/addcover", {
      method: "POST", body: JSON.stringify({ path: targetFile }),
    });
    covers = res.covers;
    setCoverOpen(true);
    openThumb = res.name;
    renderCovers();
  } catch (e) {
    showModal("Errore", String(e), [["Chiudi", hideModal]]);
  }
}

async function useAsEpubCover(name) {
  const c = covers.find((x) => x.name === name);
  if (!c || c.kind !== "image") {
    alert(t("cover_epub_img_note"));
    return;
  }
  if (!confirm(t("use_as_cover_confirm", { name: c.name }))) return;
  try {
    const res = await api("/api/useasepubcover", {
      method: "POST", body: JSON.stringify({ name }),
    });
    state.book = res.book;
    currentFile = "";
    renderBook();
    if (state.book.chapters.length) await openChapter(state.book.chapters[0].file);
  } catch (e) {
    showModal("Errore", String(e), [["Chiudi", hideModal]]);
  }
}

// sezione Copertine richiudibile (scelta memorizzata nel browser)
function setCoverOpen(open) {
  $("coverSection").classList.toggle("collapsed", !open);
  $("coverHead").querySelector(".arrow").textContent = open ? "▾" : "▸";
  try { localStorage.setItem("pubcraft.coversOpen", open ? "1" : "0"); } catch {}
}

/* ------------------------------------------------------------- viste doc */

function setView(mode) {
  const panes = $("panes");
  panes.classList.toggle("edit", mode === "edit");
  panes.classList.toggle("split", mode === "split");
  panes.classList.toggle("prevOnly", mode === "prev");
  [["vwEdit", "edit"], ["vwSplit", "split"], ["vwPrev", "prev"]].forEach(([id, m]) =>
    $(id).classList.toggle("on", m === mode));
}

/* ----------------------------------------------------------------- theme */
let isDarkMode = false;
try {
  isDarkMode = localStorage.getItem("pubcraft.theme") === "dark";
} catch {}

function updateThemeUI() {
  document.body.classList.toggle("dark-mode", isDarkMode);
  document.documentElement.classList.toggle("dark-mode", isDarkMode);

  const topbarThemeBtn = $("btnToggleTheme");
  if (topbarThemeBtn) {
    topbarThemeBtn.title = t("theme_toggle_title");
    topbarThemeBtn.setAttribute("aria-label", t("theme_toggle_title"));
    const label = $("themeToggleLabel");
    if (label) {
      label.textContent = isDarkMode ? t("theme_light") : t("theme_dark");
    }
    const icon = $("themeToggleIcon");
    if (icon) {
      icon.textContent = isDarkMode ? "☀️" : "🌙";
    }
    topbarThemeBtn.classList.toggle("active", isDarkMode);
  }

  const focusThemeBtn = $("btnFocusTheme");
  if (focusThemeBtn) {
    focusThemeBtn.title = t("theme_toggle_title");
    focusThemeBtn.classList.toggle("active", isDarkMode);
    const hudLabel = $("focusThemeLabel");
    if (hudLabel) {
      hudLabel.textContent = isDarkMode ? "☀️ " + t("theme_light") : "🌙 " + t("theme_dark");
    }
  }

  if (typeof renderPreview === "function" && state && state.vault && currentFile) {
    renderPreview();
  }
}

function applyTheme(dark) {
  isDarkMode = !!dark;
  try {
    localStorage.setItem("pubcraft.theme", isDarkMode ? "dark" : "light");
  } catch {}
  updateThemeUI();
}

function toggleTheme() {
  applyTheme(!isDarkMode);
}

function initTheme() {
  try {
    isDarkMode = localStorage.getItem("pubcraft.theme") === "dark";
  } catch {}
  updateThemeUI();
}

/* -------------------------------- Modalità Non Disturbo (Focus / Typewriter Mode) */

let isFocusMode = false;
let focusTypewriter = false;
let isSuperFocus = false;
let focusDimMode = "off"; // "off" | "sentence" | "paragraph"
try {
  focusTypewriter = localStorage.getItem("pubcraft.focusTypewriter") === "1";
  isSuperFocus = localStorage.getItem("pubcraft.superFocus") === "1";
  focusDimMode = localStorage.getItem("pubcraft.focusDimMode") || "off";
  if (!["off", "sentence", "paragraph"].includes(focusDimMode)) {
    focusDimMode = "off";
  }
} catch {}
let hudDimTimer = null;
let mirrorDiv = null;
let centerScrollRaf = null;

function getFocusRange(text, sel, mode) {
  if (!text || text.length === 0) return [0, 0];
  const clampedSel = Math.max(0, Math.min(sel, text.length));

  if (mode === "paragraph") {
    // Cerca inizio del paragrafo (separato da doppio a capo o inizio testo)
    const before = text.slice(0, clampedSel);
    const prevBreak = before.lastIndexOf("\n\n");
    const start = prevBreak === -1 ? 0 : prevBreak + 2;

    // Cerca fine del paragrafo
    const after = text.slice(clampedSel);
    const nextBreak = after.indexOf("\n\n");
    const end = nextBreak === -1 ? text.length : clampedSel + nextBreak;
    return [start, end];
  }

  if (mode === "sentence") {
    // Determina prima il paragrafo contenitore
    const beforePara = text.slice(0, clampedSel);
    const prevBreak = beforePara.lastIndexOf("\n\n");
    const pStart = prevBreak === -1 ? 0 : prevBreak + 2;

    const afterPara = text.slice(clampedSel);
    const nextBreak = afterPara.indexOf("\n\n");
    const pEnd = nextBreak === -1 ? text.length : clampedSel + nextBreak;

    const paraText = text.slice(pStart, pEnd);
    const relSel = clampedSel - pStart;

    // Isola la frase all'interno del paragrafo
    const re = /[^.!?\n]+(?:[.!?]+(?=[\s\n]|$)|[\n]|$)/g;
    let match;
    let sStart = 0;
    let sEnd = paraText.length;
    while ((match = re.exec(paraText)) !== null) {
      const matchEnd = match.index + match[0].length;
      if (relSel >= match.index && relSel <= matchEnd) {
        sStart = match.index;
        sEnd = matchEnd;
        break;
      }
    }
    return [pStart + sStart, pStart + sEnd];
  }

  return [0, text.length];
}

function renderFocusDimming() {
  const backdrop = $("focusBackdrop");
  if (!backdrop) return;
  if (!isFocusMode || focusDimMode === "off") {
    document.body.classList.remove("focus-dim-active");
    backdrop.innerHTML = "";
    return;
  }

  document.body.classList.add("focus-dim-active");
  const val = ta.value || "";
  const sel = ta.selectionStart || 0;
  const [start, end] = getFocusRange(val, sel, focusDimMode);

  const before = val.slice(0, start);
  const current = val.slice(start, end);
  const after = val.slice(end);

  let html = `<span class="focus-dimmed">${esc(before)}</span>` +
             `<span class="focus-current">${esc(current)}</span>` +
             `<span class="focus-dimmed">${esc(after)}</span>`;
  if (val.endsWith("\n")) {
    html += "<br>";
  }
  backdrop.innerHTML = html;
  backdrop.scrollTop = ta.scrollTop;
}

function cycleFocusDim() {
  if (focusDimMode === "off") {
    focusDimMode = "sentence";
  } else if (focusDimMode === "sentence") {
    focusDimMode = "paragraph";
  } else {
    focusDimMode = "off";
  }
  try {
    localStorage.setItem("pubcraft.focusDimMode", focusDimMode);
  } catch {}
  document.body.classList.toggle("focus-dim-active", focusDimMode !== "off");
  updateFocusHud();
  renderFocusDimming();
  pokeFocusHud();
  ta.focus();
}

function enterFocusMode() {
  if (!state.book || !currentFile) return;
  isFocusMode = true;
  document.body.classList.add("focus-mode");
  document.body.classList.toggle("focus-typewriter", focusTypewriter);
  document.body.classList.toggle("focus-super-focus", isSuperFocus);
  document.body.classList.toggle("focus-dim-active", focusDimMode !== "off");

  $("focusHud")?.classList.remove("hidden");
  updateFocusHud();
  pokeFocusHud();
  if (focusDimMode !== "off") {
    renderFocusDimming();
  }

  if (!document.fullscreenElement && document.documentElement.requestFullscreen) {
    document.documentElement.requestFullscreen().catch(() => {});
  }

  ta.focus();
  if (isSuperFocus) {
    setTimeout(() => centerActiveLine(true), 60);
  }
}

function exitFocusMode() {
  isFocusMode = false;
  document.body.classList.remove("focus-mode", "focus-typewriter", "focus-super-focus", "focus-dim-active");
  $("focusHud")?.classList.add("hidden");
  $("editorToolbar")?.classList.remove("hud-dimmed");
  const backdrop = $("focusBackdrop");
  if (backdrop) backdrop.innerHTML = "";

  if (document.fullscreenElement && document.exitFullscreen) {
    document.exitFullscreen().catch(() => {});
  }

  ta.focus();
}

function toggleFocusMode() {
  if (isFocusMode) {
    exitFocusMode();
  } else {
    enterFocusMode();
  }
}

function toggleFocusFont() {
  focusTypewriter = !focusTypewriter;
  try { localStorage.setItem("pubcraft.focusTypewriter", focusTypewriter ? "1" : "0"); } catch {}
  document.body.classList.toggle("focus-typewriter", focusTypewriter);
  updateFocusHud();
  pokeFocusHud();
  if (focusDimMode !== "off") {
    renderFocusDimming();
  }
  ta.focus();
  if (isSuperFocus) {
    setTimeout(() => centerActiveLine(true), 60);
  }
}

function toggleSuperFocus() {
  isSuperFocus = !isSuperFocus;
  try { localStorage.setItem("pubcraft.superFocus", isSuperFocus ? "1" : "0"); } catch {}
  document.body.classList.toggle("focus-super-focus", isSuperFocus);
  updateFocusHud();
  pokeFocusHud();
  ta.focus();
  if (isSuperFocus) {
    setTimeout(() => centerActiveLine(true), 60);
  }
}

function updateFocusChapterSelect() {
  const sel = $("focusChapterSelect");
  if (!sel || !state.book || !Array.isArray(state.book.chapters)) return;
  const currentVal = currentFile;
  sel.innerHTML = "";
  state.book.chapters.forEach((ch, idx) => {
    const opt = document.createElement("option");
    opt.value = ch.file;
    const num = idx + 1;
    const name = ch.title || `${t("chapter")} ${num}`;
    opt.textContent = `${num}. ${name}`;
    if (ch.file === currentVal) opt.selected = true;
    sel.appendChild(opt);
  });
}

async function handleFocusChapterSelect(e) {
  const targetFile = e.target.value;
  if (!targetFile || targetFile === currentFile) return;
  if (dirty) {
    await flushSave();
  }
  await openChapter(targetFile);
  pokeFocusHud();
  updateFocusHud();
  if (focusDimMode !== "off") {
    renderFocusDimming();
  }
  ta.focus();
  if (isSuperFocus) {
    setTimeout(() => centerActiveLine(true), 120);
  }
}

function updateFocusHud() {
  if (!isFocusMode) return;
  const chTitle = $("curName")?.textContent || currentTitle || t("untitled");
  if ($("focusChapterTitle")) $("focusChapterTitle").textContent = chTitle;
  updateFocusChapterSelect();

  if ($("focusWordCount")) $("focusWordCount").textContent = $("wordCount")?.textContent || "0 parole";
  if ($("focusSaveStatus")) $("focusSaveStatus").textContent = $("saveState")?.textContent || "";
  if ($("focusFontLabel")) {
    $("focusFontLabel").textContent = focusTypewriter ? t("focus_font_serif") : t("focus_font_typewriter");
  }
  if ($("focusDimLabel")) {
    if (focusDimMode === "sentence") {
      $("focusDimLabel").textContent = t("focus_dim_sentence");
    } else if (focusDimMode === "paragraph") {
      $("focusDimLabel").textContent = t("focus_dim_paragraph");
    } else {
      $("focusDimLabel").textContent = t("focus_dim_off");
    }
  }
  $("btnFocusDim")?.classList.toggle("active", focusDimMode !== "off");
  if ($("superFocusLabel")) {
    $("superFocusLabel").textContent = isSuperFocus ? t("focus_super_focus_on") : t("focus_super_focus_off");
  }
  $("btnSuperFocus")?.classList.toggle("active", isSuperFocus);
  if ($("focusThemeLabel")) {
    $("focusThemeLabel").textContent = isDarkMode ? "☀️ " + t("theme_light") : "🌙 " + t("theme_dark");
  }
  $("btnFocusTheme")?.classList.toggle("active", isDarkMode);
}

function pokeFocusHud() {
  if (!isFocusMode) return;
  const hud = $("focusHud");
  const tb = $("editorToolbar");
  if (hud) hud.classList.remove("hud-dimmed");
  if (tb) tb.classList.remove("hud-dimmed");
  clearTimeout(hudDimTimer);
  hudDimTimer = setTimeout(() => {
    if (isFocusMode) {
      if (!hud?.matches(":hover")) hud?.classList.add("hud-dimmed");
      if (!tb?.matches(":hover")) tb?.classList.add("hud-dimmed");
    }
  }, 2600);
}

function getCaretOffsetY() {
  if (!mirrorDiv) {
    mirrorDiv = document.createElement("div");
    mirrorDiv.id = "taCaretMirror";
    mirrorDiv.style.visibility = "hidden";
    mirrorDiv.style.position = "absolute";
    mirrorDiv.style.pointerEvents = "none";
    mirrorDiv.style.top = "-99999px";
    mirrorDiv.style.left = "-99999px";
    document.body.appendChild(mirrorDiv);
  }
  const st = window.getComputedStyle(ta);
  mirrorDiv.style.fontFamily = st.fontFamily;
  mirrorDiv.style.fontSize = st.fontSize;
  mirrorDiv.style.lineHeight = st.lineHeight;
  mirrorDiv.style.letterSpacing = st.letterSpacing;
  mirrorDiv.style.fontWeight = st.fontWeight;
  mirrorDiv.style.width = ta.clientWidth + "px";
  mirrorDiv.style.paddingLeft = st.paddingLeft;
  mirrorDiv.style.paddingRight = st.paddingRight;
  mirrorDiv.style.boxSizing = "border-box";
  mirrorDiv.style.whiteSpace = "pre-wrap";
  mirrorDiv.style.wordBreak = "break-word";

  const sel = ta.selectionStart || 0;
  const before = ta.value.substring(0, sel);
  mirrorDiv.textContent = before;
  const caretMarker = document.createElement("span");
  caretMarker.textContent = "X";
  mirrorDiv.appendChild(caretMarker);
  return caretMarker.offsetTop;
}

function centerActiveLine(smooth = false) {
  if (!isFocusMode || !isSuperFocus) return;
  cancelAnimationFrame(centerScrollRaf);
  centerScrollRaf = requestAnimationFrame(() => {
    const caretY = getCaretOffsetY();
    const currentScroll = ta.scrollTop;
    if (Math.abs(currentScroll - caretY) > 2) {
      if (smooth) {
        ta.scrollTo({ top: caretY, behavior: "smooth" });
      } else {
        ta.scrollTop = caretY;
      }
    }
    const bd = $("focusBackdrop");
    if (bd && isFocusMode && focusDimMode !== "off") {
      bd.scrollTop = ta.scrollTop;
    }
  });
}

function keepTypewriterCaretCentered() {
  if (!isFocusMode) return;
  if (isSuperFocus) {
    centerActiveLine(false);
  }
}

/* ------------------------------------------------------------- topbar title */

let isCommittingTitle = false;

function initTopbarTitleEditing() {
  const titleSpan = $("topbarBookTitle");
  const titleInput = $("topbarBookTitleInput");
  const sidebarTitle = $("bookTitle");
  if (!titleSpan || !titleInput) return;

  function startEditing() {
    if (!state.vault || !state.book || isProjectSwitching || isBuilding) return;
    titleInput.value = state.book.title || "";
    titleSpan.classList.add("hidden");
    titleInput.classList.remove("hidden");
    titleInput.focus();
    titleInput.select();
  }

  async function commitRename() {
    if (isCommittingTitle || titleInput.classList.contains("hidden")) return;
    isCommittingTitle = true;
    const newTitle = titleInput.value.trim();
    titleInput.classList.add("hidden");
    titleSpan.classList.remove("hidden");

    if (!newTitle || !state.book || newTitle === state.book.title) {
      if (state.book && state.book.title) {
        titleSpan.textContent = state.book.title;
      }
      isCommittingTitle = false;
      return;
    }

    try {
      const res = await api("/api/settitle", {
        method: "POST",
        body: JSON.stringify({ title: newTitle }),
      });
      if (res && res.book) {
        state.book = res.book;
        titleSpan.textContent = res.book.title;
        if (sidebarTitle) sidebarTitle.textContent = res.book.title;
        document.title = `${res.book.title} — PubCraft`;
      }
    } catch (err) {
      showModal(t("modal_error"), String(err), [[t("modal_close"), hideModal]]);
      if (state.book && state.book.title) {
        titleSpan.textContent = state.book.title;
      }
    } finally {
      isCommittingTitle = false;
    }
  }

  function cancelRename() {
    isCommittingTitle = true;
    titleInput.classList.add("hidden");
    titleSpan.classList.remove("hidden");
    if (state.book && state.book.title) {
      titleSpan.textContent = state.book.title;
    }
    setTimeout(() => { isCommittingTitle = false; }, 50);
  }

  titleSpan.addEventListener("click", startEditing);
  if (sidebarTitle) {
    sidebarTitle.style.cursor = "pointer";
    sidebarTitle.title = t("topbar_title_tooltip");
    sidebarTitle.addEventListener("click", startEditing);
  }

  titleInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      commitRename();
    } else if (e.key === "Escape") {
      e.preventDefault();
      cancelRename();
    }
  });

  titleInput.addEventListener("blur", () => {
    commitRename();
  });
}

/* ----------------------------------------------------------------- notebook */

let notesData = [];
let currentNoteFile = "";
let currentNoteVault = "";
let currentNoteVersion = "";
let noteNavId = 0;
let noteRevision = 0;
let lastSavedNoteRevision = 0;
let noteDirty = false;
let noteSaveTimer = null;
let noteSavePromise = null;
let isSavingNote = false;
let isLoadingNote = false;

function setNoteInputsEditable(editable) {
  const titleInp = $("noteTitleInput");
  const contentInp = $("noteContentInput");
  if (titleInp) {
    titleInp.readOnly = !editable;
    titleInp.classList.toggle("note-input-locked", !editable);
  }
  if (contentInp) {
    contentInp.readOnly = !editable;
    contentInp.classList.toggle("note-input-locked", !editable);
  }
}

function setNotebookOpen(open) {
  if (!$("notebookSection")) return;
  $("notebookSection").classList.toggle("collapsed", !open);
  if ($("notebookBody")) $("notebookBody").classList.toggle("hidden", !open);
  if ($("notebookArrow")) $("notebookArrow").textContent = open ? "▾" : "▸";
  try { localStorage.setItem("pubcraft.notebookOpen", open ? "1" : "0"); } catch {}
}

async function loadNotes() {
  if (!state || !state.vault) {
    notesData = [];
    renderNotesList();
    renderNoteTabs();
    return;
  }
  try {
    const res = await api("/api/notes");
    notesData = res.notes || [];
    renderNotesList();
    renderNoteTabs();
    if (currentNoteFile) {
      const exists = notesData.some(n => n.file === currentNoteFile);
      if (!exists && notesData.length > 0) {
        openNote(notesData[0].file);
      } else if (!exists) {
        currentNoteFile = "";
        resetNoteEditor();
      }
    }
  } catch (err) {
    console.error("loadNotes error:", err);
  }
}

function renderNotesList() {
  const list = $("noteList");
  const badge = $("noteBadge");
  if (badge) {
    badge.textContent = String(notesData.length);
    badge.classList.toggle("hidden", notesData.length === 0);
  }
  if (!list) return;
  list.innerHTML = "";
  if (notesData.length === 0) {
    const emptyLi = document.createElement("li");
    emptyLi.className = "note-item-empty";
    emptyLi.textContent = t("note_empty");
    list.appendChild(emptyLi);
    return;
  }
  notesData.forEach(n => {
    const li = document.createElement("li");
    li.className = "note-item" + (n.file === currentNoteFile ? " active" : "");
    li.dataset.file = n.file;

    const icon = document.createElement("span");
    icon.className = "note-icon";
    icon.textContent = "📝";

    const title = document.createElement("span");
    title.className = "note-title";
    title.textContent = n.title || t("note_untitled");
    title.title = n.title || "";

    li.append(icon, title);
    li.addEventListener("click", () => {
      openNotebookDrawer(n.file);
    });
    list.appendChild(li);
  });
}

function renderNoteTabs() {
  const container = $("drawerNoteTabs");
  if (!container) return;
  container.innerHTML = "";
  if (notesData.length === 0) return;
  notesData.forEach(n => {
    const tab = document.createElement("button");
    tab.type = "button";
    tab.className = "note-tab" + (n.file === currentNoteFile ? " active" : "");
    tab.textContent = n.title || t("note_untitled");
    tab.title = n.title || "";
    tab.addEventListener("click", () => {
      if (currentNoteFile !== n.file) {
        if (noteSaveTimer) saveCurrentNote();
        openNote(n.file);
      }
    });
    container.appendChild(tab);
  });
}

function setNoteStatus(msg, isError = false) {
  const el = $("noteSaveStatus");
  if (!el) return;
  el.textContent = msg;
  el.style.color = isError ? "#a03028" : "var(--muted)";
}

function resetNoteEditor() {
  currentNoteVersion = "";
  if ($("noteTitleInput")) $("noteTitleInput").value = "";
  if ($("noteContentInput")) $("noteContentInput").value = "";
  setNoteInputsEditable(true);
  setNoteStatus("");
  noteDirty = false;
  notifyClientDirty();
}

function toggleNotebookDrawer() {
  const drawer = $("notebookDrawer");
  if (!drawer) return;
  const isHidden = drawer.classList.contains("hidden");
  if (isHidden) {
    openNotebookDrawer();
  } else {
    closeNotebookDrawer();
  }
}

function openNotebookDrawer(noteFileToSelect) {
  const drawer = $("notebookDrawer");
  if (!drawer) return;
  drawer.classList.remove("hidden");
  $("btnToggleNotebook")?.classList.add("active");
  if (noteFileToSelect) {
    openNote(noteFileToSelect);
  } else if (!currentNoteFile && notesData.length > 0) {
    openNote(notesData[0].file);
  } else if (!currentNoteFile) {
    resetNoteEditor();
  }
}

function closeNotebookDrawer() {
  const drawer = $("notebookDrawer");
  if (!drawer) return;
  drawer.classList.add("hidden");
  $("btnToggleNotebook")?.classList.remove("active");
  if (noteDirty || noteSaveTimer) {
    saveCurrentNote();
  }
}

async function openNote(file) {
  if (isLoadingNote) return false;
  if (noteDirty || noteSaveTimer || noteRevision > lastSavedNoteRevision) {
    const ok = await flushNoteSave();
    if (!ok) {
      setNoteStatus(t("save_error"), true);
      return false;
    }
  }
  const myNavId = ++noteNavId;
  const myVault = (state && state.vault) || "";
  isLoadingNote = true;
  setNoteInputsEditable(false);
  setNoteStatus(t("note_loading"));
  try {
    const res = await api("/api/note?file=" + encodeURIComponent(file));
    if (myNavId !== noteNavId || !state || state.vault !== myVault) {
      return false;
    }
    currentNoteFile = file;
    currentNoteVault = myVault;
    currentNoteVersion = res.version || "";
    if ($("noteTitleInput")) $("noteTitleInput").value = res.title || "";
    if ($("noteContentInput")) $("noteContentInput").value = res.content || "";
    noteDirty = false;
    noteRevision++;
    lastSavedNoteRevision = noteRevision;
    notifyClientDirty();
    setNoteStatus("");
    renderNotesList();
    renderNoteTabs();
    return true;
  } catch (err) {
    if (myNavId === noteNavId) {
      setNoteStatus(t("load_error"), true);
    }
    return false;
  } finally {
    if (myNavId === noteNavId) {
      isLoadingNote = false;
      setNoteInputsEditable(true);
    }
  }
}

async function newNote() {
  if (noteDirty || noteSaveTimer || noteRevision > lastSavedNoteRevision) {
    const ok = await flushNoteSave();
    if (!ok) {
      setNoteStatus(t("save_error"), true);
      showModal(t("save_failed"), t("save_failed_change_ch"), [
        [t("btn_retry_save"), async () => { hideModal(); await newNote(); }],
        [t("btn_cancel"), hideModal],
      ]);
      return;
    }
  }
  const defaultTitle = t("note_untitled");
  try {
    const res = await api("/api/note", {
      method: "POST",
      body: JSON.stringify({
        file: "",
        title: defaultTitle,
        content: "",
      }),
    });
    if (res.ok && res.file) {
      currentNoteFile = res.file;
      currentNoteVault = (state && state.vault) || "";
      currentNoteVersion = res.version || "";
      noteDirty = false;
      noteRevision++;
      lastSavedNoteRevision = noteRevision;
      await notifyClientDirty();
      await loadNotes();
      openNotebookDrawer(res.file);
      if ($("noteTitleInput")) {
        $("noteTitleInput").value = res.title;
        $("noteTitleInput").focus();
        $("noteTitleInput").select();
      }
      if ($("noteContentInput")) {
        $("noteContentInput").value = "";
      }
    }
  } catch (err) {
    showModal(t("modal_error"), String(err), [[t("modal_close"), hideModal]]);
  }
}

function queueSaveCurrentNote() {
  if (isLoadingNote) return;
  noteRevision++;
  noteDirty = true;
  notifyClientDirty();
  setNoteStatus(t("note_unsaved"));
  clearTimeout(noteSaveTimer);
  noteSaveTimer = setTimeout(saveCurrentNote, 600);
}

function handleNoteConflict(title, content) {
  showModal(
    "Conflitto di modifica",
    "Questa nota è stata modificata da un'altra sessione o all'esterno. La tua bozza attuale è preservata. Scegli come procedere:",
    [
      ["Salva come copia", async () => {
        hideModal();
        await saveNoteAsCopy(title, content);
      }],
      ["Sovrascrivi", async () => {
        hideModal();
        await saveCurrentNote({ force: true });
      }],
      ["Annulla", hideModal]
    ]
  );
}

async function saveNoteAsCopy(title, content) {
  try {
    const copyTitle = title + " (Copia)";
    const res = await api("/api/note", {
      method: "POST",
      body: JSON.stringify({
        file: "",
        title: copyTitle,
        content: content,
      }),
    });
    if (res && res.ok && res.file) {
      currentNoteFile = res.file;
      currentNoteVault = (state && state.vault) || "";
      currentNoteVersion = res.version || "";
      noteDirty = false;
      noteRevision++;
      lastSavedNoteRevision = noteRevision;
      await notifyClientDirty();
      await loadNotes();
      setNoteStatus(t("save_saved"));
      if ($("noteTitleInput")) $("noteTitleInput").value = copyTitle;
    }
  } catch (err) {
    showModal(t("modal_error"), String(err), [[t("modal_close"), hideModal]]);
  }
}

async function saveCurrentNote(opts = {}) {
  clearTimeout(noteSaveTimer);
  noteSaveTimer = null;
  if (isLoadingNote) return false;
  if (!currentNoteFile && !$("noteTitleInput")?.value?.trim() && !$("noteContentInput")?.value?.trim()) {
    noteDirty = false;
    notifyClientDirty();
    return true;
  }
  if (noteSavePromise) return noteSavePromise;

  const targetFile = currentNoteFile;
  const targetVault = (state && state.vault) || "";
  const targetRev = noteRevision;
  const targetBaseVersion = currentNoteVersion;
  const force = Boolean(opts && opts.force);
  const title = $("noteTitleInput")?.value?.trim() || t("note_untitled");
  const content = $("noteContentInput")?.value || "";

  noteSavePromise = (async () => {
    setNoteStatus(t("save_saving"));
    isSavingNote = true;
    try {
      const res = await api("/api/note", {
        method: "POST",
        body: JSON.stringify({
          file: targetFile,
          title: title,
          content: content,
          base_version: targetBaseVersion,
          force: force,
        }),
      });
      if (!state || state.vault !== targetVault) {
        throw new Error("Progetto cambiato durante il salvataggio della nota");
      }
      if (res && res.ok) {
        if (targetFile === currentNoteFile || !currentNoteFile) {
          currentNoteFile = res.file;
          currentNoteVault = targetVault;
          currentNoteVersion = res.version || "";
          if (targetRev > lastSavedNoteRevision) {
            lastSavedNoteRevision = targetRev;
          }
          if (noteRevision <= lastSavedNoteRevision) {
            noteDirty = false;
            await notifyClientDirty();
          }
          const idx = notesData.findIndex(n => n.file === currentNoteFile);
          if (idx >= 0) {
            notesData[idx].title = res.title;
            notesData[idx].preview = content.replace(/\n/g, " ").slice(0, 80);
          }
          renderNotesList();
          renderNoteTabs();
          setNoteStatus(t("save_saved"));
          setTimeout(() => {
            if (!noteSaveTimer && !isSavingNote && !noteDirty) setNoteStatus("");
          }, 1500);
        }
        return true;
      }
      throw new Error(res.error || "Errore salvataggio nota");
    } catch (err) {
      if (err && (err.status === 409 || String(err).includes("409") || String(err).includes("NOTE_CONFLICT"))) {
        setNoteStatus(t("conflict_detected") || "Conflitto rilevato", true);
        handleNoteConflict(title, content);
      } else {
        setNoteStatus(t("save_error"), true);
      }
      return false;
    } finally {
      isSavingNote = false;
      noteSavePromise = null;
    }
  })();
  return noteSavePromise;
}

async function flushNoteSave() {
  clearTimeout(noteSaveTimer);
  noteSaveTimer = null;
  if (noteSavePromise) {
    const pending = noteSavePromise;
    const ok = await pending;
    if (noteSavePromise === pending) noteSavePromise = null;
    if (!ok && !noteDirty) return false;
  }
  while (noteDirty || (currentNoteFile && noteRevision > lastSavedNoteRevision)) {
    const ok = await saveCurrentNote();
    if (!ok) return false;
  }
  return !noteDirty && (noteRevision <= lastSavedNoteRevision);
}

async function deleteCurrentNote() {
  if (!currentNoteFile) return;
  const fileToDelete = currentNoteFile;
  const noteTitle = $("noteTitleInput")?.value?.trim() || fileToDelete;
  showModal(
    t("note_del_title"),
    t("note_del_confirm", { title: noteTitle }),
    [
      [t("btn_cancel"), hideModal],
      [t("btn_delete"), async () => {
        hideModal();
        clearTimeout(noteSaveTimer);
        noteSaveTimer = null;
        try {
          await api("/api/delnote", {
            method: "POST",
            body: JSON.stringify({ file: fileToDelete }),
          });
          if (currentNoteFile === fileToDelete) {
            currentNoteFile = "";
            resetNoteEditor();
          }
          await loadNotes();
          if (notesData.length > 0) {
            openNote(notesData[0].file);
          } else {
            closeNotebookDrawer();
          }
        } catch (err) {
          showModal(t("modal_error"), String(err), [[t("modal_close"), hideModal]]);
        }
      }],
    ]
  );
}

function insertNoteInChapter() {
  if (!currentFile || !ta) {
    showModal(t("modal_error"), t("save_failed_change_ch"), [[t("modal_close"), hideModal]]);
    return;
  }
  const noteContent = $("noteContentInput")?.value?.trim();
  if (!noteContent) return;

  const start = typeof ta.selectionStart === "number" ? ta.selectionStart : ta.value.length;
  const end = typeof ta.selectionEnd === "number" ? ta.selectionEnd : start;
  const val = ta.value;

  const before = val.substring(0, start);
  const after = val.substring(end);

  const prefix = (before.length > 0 && !before.endsWith("\n\n")) ? (before.endsWith("\n") ? "\n" : "\n\n") : "";
  const suffix = (after.length > 0 && !after.startsWith("\n\n")) ? (after.startsWith("\n") ? "\n" : "\n\n") : "";

  const insertion = prefix + noteContent + suffix;
  ta.value = before + insertion + after;
  ta.selectionStart = ta.selectionEnd = before.length + insertion.length;
  ta.focus();

  queueSave();
  updateWordCount();
  renderPreview();

  setNoteStatus(t("note_inserted"));
  setTimeout(() => {
    if (!noteSaveTimer && !isSavingNote) setNoteStatus("");
  }, 2000);
}

async function convertNoteToChapter() {
  if (!currentNoteFile) return;
  if (noteDirty || noteSaveTimer || noteRevision > lastSavedNoteRevision) {
    const ok = await flushNoteSave();
    if (!ok) {
      setNoteStatus(t("save_error"), true);
      return;
    }
  }
  showModal(
    t("note_convert"),
    t("note_convert_confirm"),
    [
      [t("btn_cancel"), hideModal],
      [t("note_convert"), async () => {
        hideModal();
        try {
          const res = await api("/api/note-to-chapter", {
            method: "POST",
            body: JSON.stringify({ file: currentNoteFile, keepNote: false }),
          });
          if (res.ok && res.book) {
            state.book = res.book;
            renderBook();
            currentNoteFile = "";
            resetNoteEditor();
            await loadNotes();
            closeNotebookDrawer();
            if (res.chapterFile) {
              await openChapter(res.chapterFile, true);
            }
          }
        } catch (err) {
          showModal(t("modal_error"), String(err), [[t("modal_close"), hideModal]]);
        }
      }],
    ]
  );
}

function initNotebook() {
  $("notebookHead")?.addEventListener("click", () => {
    setNotebookOpen($("notebookSection")?.classList.contains("collapsed"));
  });
  try {
    setNotebookOpen(localStorage.getItem("pubcraft.notebookOpen") === "1");
  } catch {}

  $("btnAddNote")?.addEventListener("click", () => {
    openNotebookDrawer();
    newNote();
  });
  $("btnToggleNotebook")?.addEventListener("click", toggleNotebookDrawer);
  $("btnDrawerNewNote")?.addEventListener("click", newNote);
  $("btnDrawerClose")?.addEventListener("click", closeNotebookDrawer);

  $("noteTitleInput")?.addEventListener("input", queueSaveCurrentNote);
  $("noteContentInput")?.addEventListener("input", queueSaveCurrentNote);
  $("btnDeleteNote")?.addEventListener("click", deleteCurrentNote);
  $("btnInsertNoteToChapter")?.addEventListener("click", insertNoteInChapter);
  $("btnConvertNoteToChapter")?.addEventListener("click", convertNoteToChapter);

  $("notebookDrawer")?.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      closeNotebookDrawer();
    }
  });
}

/* ------------------------------------------------------------------ init */

$("btnOpen")?.addEventListener("click", openModal);
$("btnOpenEmpty")?.addEventListener("click", openModal);
$("btnNewProject")?.addEventListener("click", showNewProjectModal);
$("btnNewProjectEmpty")?.addEventListener("click", showNewProjectModal);
$("btnOpenNewProject")?.addEventListener("click", showNewProjectModal);
$("btnNpCancel").addEventListener("click", () => $("newProjectModal").classList.add("hidden"));
$("newProjectModal").addEventListener("click", e => {
  if (e.target === $("newProjectModal")) $("newProjectModal").classList.add("hidden");
});
$("btnNpBrowseDir").addEventListener("click", async () => {
  const dir = await chooseDir(t("np_parent_dir_label"));
  if (dir) $("npParentDir").value = typeof dir === "string" ? dir : (dir.path || dir.dir);
});
$("npTitle").addEventListener("input", () => {
  if (autoProjectFolder) $("npFolderName").value = slugProjectFolder($("npTitle").value);
});
$("npFolderName").addEventListener("input", () => { autoProjectFolder = false; });
$("btnNpCreate").addEventListener("click", createNewProject);
$("btnOpenFolder").addEventListener("click", openVaultFolder);
if ($("btnOpenArchive")) $("btnOpenArchive").addEventListener("click", importArchive);
$("btnOpenDocx").addEventListener("click", importDocx);
$("btnOpenCancel").addEventListener("click", hideOpenModal);
$("openModal").addEventListener("click", (e) => {
  if (e.target === $("openModal")) hideOpenModal();
});
$("btnNew").addEventListener("click", newChapter);

function isDefaultSectionTitle(title) {
  const keys = ["default_dedication", "default_epigraph", "default_about_author", "default_acknowledgments", "default_preface"];
  return Object.values(I18N).some(dict => keys.some(key => dict[key] === title));
}

$("newChapterType").addEventListener("change", (e) => {
  const type = e.target.value;
  const titleInput = $("newChapterTitle");
  const defaults = {
    dedication: "default_dedication",
    epigraph: "default_epigraph",
    "about-author": "default_about_author",
    acknowledgments: "default_acknowledgments",
    preface: "default_preface",
  };
  if (defaults[type]) titleInput.value = t(defaults[type]);
  else if (isDefaultSectionTitle(titleInput.value)) titleInput.value = "";
});

$("btnNewChapterCancel").addEventListener("click", hideNewChapterModal);
$("newChapterModal").addEventListener("click", (e) => {
  if (e.target === $("newChapterModal")) hideNewChapterModal();
});

$("btnNewChapterConfirm").addEventListener("click", async () => {
  if (isProjectSwitching || isBuilding) return;
  const title = $("newChapterTitle").value.trim();
  const type = $("newChapterType").value;
  if (!title) {
    alert(t("enter_section_title"));
    return;
  }
  hideNewChapterModal();
  try {
    const res = await api("/api/newchapter", {
      method: "POST", body: JSON.stringify({ title, type }),
    });
    state.book = res.book;
    renderBook();
    await openChapter(res.file);
  } catch (e) {
    showModal("Errore", String(e), [["Chiudi", hideModal]]);
  }
});

if ($("btnWatch")) {
  $("btnWatch").addEventListener("click", () => {
    window.open("/watch", "_blank");
  });
}
$("btnKindle").addEventListener("click", kindleBuild);
$("btnPrint").addEventListener("click", async () => {
  if (!state.book) return;
  await initPrintModal();
  $("printModal").classList.remove("hidden");
});
$("btnPrintCancel").addEventListener("click", () => $("printModal").classList.add("hidden"));
$("btnPickPrintDir").addEventListener("click", pickPrintDir);
$("btnPrintGo").addEventListener("click", printModalGo);
$("printModal").addEventListener("click", (e) => {
  if (e.target === $("printModal")) $("printModal").classList.add("hidden");
});
$("btnAddCover").addEventListener("click", addCover);
$("coverHead").addEventListener("click", () =>
  setCoverOpen($("coverSection").classList.contains("collapsed")));
try { setCoverOpen(localStorage.getItem("pubcraft.coversOpen") === "1"); } catch {}

$("btnFsCancel").addEventListener("click", fsCancel);
$("btnFsOpenFolder")?.addEventListener("click", fsOpenCurrentFolder);
$("btnFsSelect").addEventListener("click", fsSelect);
$("btnFsNew").addEventListener("click", newFolderInCurrent);
$("btnFsUp").addEventListener("click", () => fsBrowse(null));
document.querySelectorAll("#fsQuick .q").forEach((b) =>
  b.addEventListener("click", () => fsBrowse(b.dataset.p)));
$("fsModal").addEventListener("click", (e) => {
  if (e.target === $("fsModal")) fsCancel();
});
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    if (fsState) fsCancel();
    hideStyleMenu();
    hideOpeningMenu();
    if (!$("openModal").classList.contains("hidden")) hideOpenModal();
  }
});

function alignDropdownMenu(menuEl, containerEl) {
  if (!menuEl || !containerEl) return;
  const rect = containerEl.getBoundingClientRect();
  if (rect.left + 260 > window.innerWidth) {
    menuEl.style.left = "auto";
    menuEl.style.right = "0px";
  } else {
    menuEl.style.left = "0px";
    menuEl.style.right = "auto";
  }
}

$("btnStyle").addEventListener("click", async (e) => {
  e.stopPropagation();
  const list = $("styleList");
  if (list.classList.contains("hidden")) {
    await initStyleMenu();
    alignDropdownMenu(list, $("styleMenu"));
    list.classList.remove("hidden");
  } else hideStyleMenu();
});
document.addEventListener("click", (e) => {
  if (!$("styleMenu").contains(e.target)) hideStyleMenu();
});

$("btnOpening").addEventListener("click", (e) => {
  e.stopPropagation();
  const list = $("openingList");
  if (list.classList.contains("hidden")) {
    initOpeningMenu();
    alignDropdownMenu(list, $("openingMenu"));
    list.classList.remove("hidden");
  } else hideOpeningMenu();
});
document.addEventListener("click", (e) => {
  if (!$("openingMenu").contains(e.target)) hideOpeningMenu();
});

$("vwEdit").addEventListener("click", () => setView("edit"));
$("vwSplit").addEventListener("click", () => setView("split"));
$("vwPrev").addEventListener("click", () => setView("prev"));
$("btnFocusMode")?.addEventListener("click", toggleFocusMode);
$("btnFocusExit")?.addEventListener("click", exitFocusMode);
$("btnFocusFont")?.addEventListener("click", toggleFocusFont);
$("btnFocusDim")?.addEventListener("click", cycleFocusDim);
$("btnSuperFocus")?.addEventListener("click", toggleSuperFocus);
$("btnFocusTheme")?.addEventListener("click", toggleTheme);
$("focusChapterSelect")?.addEventListener("change", handleFocusChapterSelect);

ta.addEventListener("keyup", (e) => {
  if (isFocusMode && isSuperFocus && ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Enter", "Backspace", "Delete", "PageUp", "PageDown"].includes(e.key)) {
    centerActiveLine(false);
  }
  if (isFocusMode && focusDimMode !== "off") {
    renderFocusDimming();
  }
});

ta.addEventListener("click", () => {
  if (isFocusMode && isSuperFocus) {
    centerActiveLine(true);
  }
  if (isFocusMode && focusDimMode !== "off") {
    renderFocusDimming();
  }
});

ta.addEventListener("select", () => {
  if (isFocusMode && focusDimMode !== "off") {
    renderFocusDimming();
  }
});

ta.addEventListener("scroll", () => {
  if (isFocusMode && focusDimMode !== "off") {
    const bd = $("focusBackdrop");
    if (bd) bd.scrollTop = ta.scrollTop;
  }
});

document.addEventListener("mousemove", () => {
  if (isFocusMode) {
    pokeFocusHud();
  }
});

function sendClientCleanBeacon() {
  try {
    if (clientHeartbeatTimer) {
      clearInterval(clientHeartbeatTimer);
      clientHeartbeatTimer = null;
    }
    if (navigator.sendBeacon) {
      navigator.sendBeacon("/api/client-dirty", JSON.stringify({ client_id: tabClientId, dirty: false }));
    }
  } catch (_) {}
}

window.addEventListener("pagehide", sendClientCleanBeacon);

window.addEventListener("beforeunload", (e) => {
  if (dirty || (typeof noteDirty !== "undefined" && noteDirty)) {
    e.preventDefault();
    e.returnValue = "";
  } else {
    sendClientCleanBeacon();
  }
});

const initLang = detectLanguage();
setLanguage(initLang);
if ($("langSelect")) {
  $("langSelect").value = initLang;
  $("langSelect").addEventListener("change", (e) => {
    setLanguage(e.target.value);
  });
}


/* ==========================================================================
   Anteprima Pubblicazione (Publication Preview) — Opzione A (Manuale)
   ========================================================================== */

let pubState = {
  target: "print", // "print" | "ebook"
  printStatus: "none", // "none" | "generating" | "current" | "stale" | "error"
  ebookStatus: "none",
  printArtifact: null,
  ebookArtifact: null,
  printSettings: { trim: "6x9", bleed: false },
  viewMode: "single", // "single" | "facing"
  guides: { cut: false, safe: false },
  ebookProfile: "reader", // "reader" | "tablet" | "phone"
  ebookOrient: "portrait", // "portrait" | "landscape"
  ebookFontSize: 100, // 80 to 200
  zoom: "fit-width", // "fit-width" | "fit-page" | "0.5" | "0.75" | "1.0" | "1.25" | "1.5" | "2.0"
  currentPage: 1,
  totalPages: 0,
  pdfDoc: null,
  renderTasks: [],
  lastOpId: 0,
  lastFocusElement: null,
};

function markPublicationPreviewStale() {
  let changed = false;
  if (pubState.printStatus === "current") {
    pubState.printStatus = "stale";
    changed = true;
  }
  if (pubState.ebookStatus === "current") {
    pubState.ebookStatus = "stale";
    changed = true;
  }
  if (changed) updatePubStatusUI();
}

function resetPublicationPreview() {
  cancelPubPdfRender();
  if (pubState.pdfDoc) {
    try { pubState.pdfDoc.destroy(); } catch (_) {}
    pubState.pdfDoc = null;
  }
  pubState.printStatus = "none";
  pubState.ebookStatus = "none";
  pubState.printArtifact = null;
  pubState.ebookArtifact = null;
  pubState.ebookManifest = null;
  pubState.ebookCurrentChapterIndex = 0;
  pubState.currentPage = 1;
  pubState.totalPages = 0;
  pubState.printSettings.trim = (state && state.book && state.book.trim) || "6x9";
  pubState.printSettings.bleed = Boolean(state && state.book && state.book.bleed);
  if ($("pubTrim")) $("pubTrim").value = pubState.printSettings.trim;
  if ($("pubBleed")) $("pubBleed").checked = pubState.printSettings.bleed;
  updatePubStatusUI();
  renderPubViewer();
}

function updatePubStatusUI() {
  const currentStatus = pubState[pubState.target + "Status"];
  const badge = $("pubStatusBadge");
  const txt = $("pubStatusText");
  const updateBtn = $("btnPubUpdate");
  const dlBtn = $("btnPubDownload");

  badge.className = "pub-status-badge pub-status-" + currentStatus;
  switch (currentStatus) {
    case "none":
      txt.textContent = t("pub_status_none");
      break;
    case "generating":
      txt.textContent = t("pub_status_generating");
      break;
    case "current":
      txt.textContent = t("pub_status_current");
      break;
    case "stale":
      txt.textContent = t("pub_status_stale");
      break;
    case "error":
      txt.textContent = t("pub_status_error");
      break;
  }

  const art = pubState[pubState.target + "Artifact"];
  if (art && art.download_url) {
    dlBtn.href = art.download_url;
    dlBtn.style.display = "inline-flex";
  } else {
    dlBtn.style.display = "none";
  }

  if (currentStatus === "generating") {
    updateBtn.disabled = true;
  } else {
    updateBtn.disabled = false;
  }
}

function cancelPubPdfRender() {
  pubState.renderGeneration = (pubState.renderGeneration || 0) + 1;
  if (pubState.renderTasks && pubState.renderTasks.length) {
    pubState.renderTasks.forEach(task => {
      try { task.cancel(); } catch (_) {}
    });
    pubState.renderTasks = [];
  }
}

function showPubNotice(notice) {
  $("pubNoticeEmpty").classList.add("hidden");
  $("pubNoticeLoading").classList.add("hidden");
  $("pubNoticeError").classList.add("hidden");
  $("pubPdfViewport").classList.add("hidden");
  $("pubEbookViewport").classList.add("hidden");

  if (notice === "empty") {
    $("pubNoticeEmpty").classList.remove("hidden");
  } else if (notice === "loading") {
    $("pubNoticeLoading").classList.remove("hidden");
  } else if (notice === "error") {
    $("pubNoticeError").classList.remove("hidden");
  } else if (notice === "pdf") {
    $("pubPdfViewport").classList.remove("hidden");
  } else if (notice === "ebook") {
    $("pubEbookViewport").classList.remove("hidden");
  }
}

function showPubError(msg) {
  $("pubErrorMsg").textContent = msg || "Errore sconosciuto";
  showPubNotice("error");
}

function openPublicationPreviewModal() {
  pubState.lastFocusElement = document.activeElement;
  $("pubPreviewModal").classList.remove("hidden");

  // Project defaults are initialized on reset; reopening must preserve local choices.

  updatePubStatusUI();
  renderPubViewer();
  $("btnPubUpdate").focus();
}

function closePublicationPreviewModal() {
  $("pubPreviewModal").classList.add("hidden");
  if (pubState.lastFocusElement && typeof pubState.lastFocusElement.focus === "function") {
    pubState.lastFocusElement.focus();
  }
}

function setPublicationTarget(target) {
  if (target !== "print" && target !== "ebook") return;
  pubState.target = target;
  $("btnPubTargetPrint").classList.toggle("active", target === "print");
  $("btnPubTargetPrint").setAttribute("aria-selected", target === "print");
  $("btnPubTargetEbook").classList.toggle("active", target === "ebook");
  $("btnPubTargetEbook").setAttribute("aria-selected", target === "ebook");

  $("pubPrintBar").style.display = target === "print" ? "flex" : "none";
  $("pubEbookBar").style.display = target === "ebook" ? "flex" : "none";

  if (target === "print" && pubState.pdfDoc) {
    populatePubPageJump(pubState.totalPages);
  } else if (target === "ebook" && pubState.ebookManifest) {
    populatePubEbookChapterJump();
  }

  updatePubStatusUI();
  renderPubViewer();
}

function renderPubViewer() {
  const currentStatus = pubState[pubState.target + "Status"];
  const art = pubState[pubState.target + "Artifact"];

  if (currentStatus === "generating") {
    showPubNotice("loading");
    return;
  }
  if (currentStatus === "error" && !art) {
    showPubNotice("error");
    return;
  }
  if (!art) {
    showPubNotice("empty");
    return;
  }

  if (pubState.target === "print") {
    showPubNotice("pdf");
    if (pubState.pdfDoc) {
      renderPubPdfView();
    } else {
      loadAndRenderPubArtifact("print", art);
    }
  } else {
    showPubNotice("ebook");
    if (pubState.ebookManifest) {
      renderPubEbookView();
    } else {
      loadAndRenderPubArtifact("ebook", art);
    }
  }
}

async function updatePublicationPreview() {
  if (isBuilding || isProjectSwitching || activeProjectMutations) return;
  if (!state || !state.book || !state.vault) {
    showModal(t("modal_error"), "Apri prima una cartella libro per visualizzare l'''anteprima.", [[t("modal_close"), hideModal]]);
    return;
  }

  const curVault = state.vault;
  const target = pubState.target;
  const opId = ++pubState.lastOpId;

  // 1. Blocca UI
  isBuilding = true;
  updateProjectLock();
  pubState[target + "Status"] = "generating";
  updatePubStatusUI();
  showPubNotice("loading");

  const prevReadOnly = ta.readOnly;
  ta.readOnly = true;

  try {
    // 2. Attendere flush salvataggi prima di generare
    const saveOk = await flushSave();
    if (!saveOk) {
      pubState[target + "Status"] = pubState[target + "Artifact"] ? "stale" : "none";
      updatePubStatusUI();
      renderPubViewer();
      showModal(t("save_failed"), t("save_failed_change_ch"), [
        [t("btn_retry_save"), async () => { hideModal(); await updatePublicationPreview(); }],
        [t("btn_cancel"), hideModal],
      ]);
      return;
    }

    if (opId !== pubState.lastOpId || ((state && state.vault) || "") !== curVault) {
      return;
    }

    // 3. Invio richiesta di generazione esplicita
    const res = await api("/api/publication-preview", {
      method: "POST",
      headers: { "X-PubCraft-Vault": curVault },
      body: JSON.stringify({
        vault: curVault,
        target: target,
        request_id: String(opId),
        settings: {
          trim: pubState.printSettings.trim,
          bleed: pubState.printSettings.bleed,
        }
      })
    });

    if (opId !== pubState.lastOpId || ((state && state.vault) || "") !== curVault) {
      return;
    }

    if (!res || !res.preview_id) {
      const errMsg = (res && res.error) ? res.error : "Errore sconosciuto nella generazione anteprima";
      pubState[target + "Status"] = "error";
      updatePubStatusUI();
      showPubError(errMsg);
      return;
    }

    // 4. Successo: aggiorna artefatto e stato
    if (target === "print") {
      pubState.printArtifact = res;
      pubState.printStatus = (res.warnings && res.warnings.length) ? "stale" : "current";
    } else {
      pubState.ebookArtifact = res;
      pubState.ebookStatus = (res.warnings && res.warnings.length) ? "stale" : "current";
    }

    updatePubStatusUI();
    await loadAndRenderPubArtifact(target, res);

  } catch (err) {
    if (opId === pubState.lastOpId) {
      pubState[target + "Status"] = "error";
      updatePubStatusUI();
      showPubError(String(err));
    }
  } finally {
    if (opId === pubState.lastOpId) {
      isBuilding = false;
      updateProjectLock();
      if (currentFile && !isProjectSwitching && !isLoadingChapter) {
        ta.readOnly = false;
      } else {
        ta.readOnly = prevReadOnly;
      }
      updatePubStatusUI();
    }
  }
}

async function loadAndRenderPubArtifact(target, artifact) {
  if (target === "print") {
    cancelPubPdfRender();
    if (!window.pdfjsLib) {
      showPubError("Libreria PDF.js non disponibile.");
      return;
    }
    showPubNotice("loading");
    try {
      const res = await fetch(artifact.artifact_url);
      if (!res.ok) throw new Error("Caricamento artefatto fallito: " + res.statusText);
      const pdfBytes = await res.arrayBuffer();
      const pdfDoc = await pdfjsLib.getDocument({ data: pdfBytes }).promise;
      pubState.pdfDoc = pdfDoc;
      pubState.totalPages = pdfDoc.numPages;
      pubState.currentPage = 1;
      populatePubPageJump(pdfDoc.numPages);
      showPubNotice("pdf");
      renderPubPdfView();
    } catch (e) {
      showPubError("Rendering PDF fallito: " + e.message);
    }
  } else if (target === "ebook") {
    showPubNotice("loading");
    try {
      const res = await fetch("/api/publication-preview/" + artifact.preview_id + "/manifest");
      if (!res.ok) throw new Error("Caricamento manifest EPUB fallito");
      pubState.ebookManifest = await res.json();
      pubState.ebookCurrentChapterIndex = 0;
      populatePubEbookChapterJump();
      showPubNotice("ebook");
      renderPubEbookView();
    } catch (e) {
      showPubError("Apertura EPUB fallita: " + e.message);
    }
  }
}

function populatePubPageJump(totalPages) {
  const select = $("pubJumpChapter");
  if (!select) return;
  select.innerHTML = "";
  for (let i = 1; i <= totalPages; i++) {
    const opt = document.createElement("option");
    opt.value = String(i);
    opt.textContent = t("pub_page_indicator", { p: i, total: totalPages });
    select.appendChild(opt);
  }
  select.value = String(pubState.currentPage || 1);
}

function populatePubEbookChapterJump() {
  const select = $("pubJumpChapter");
  if (!select) return;
  select.innerHTML = "";
  const navList = (pubState.ebookManifest && pubState.ebookManifest.nav && pubState.ebookManifest.nav.length)
    ? pubState.ebookManifest.nav
    : (pubState.ebookManifest && pubState.ebookManifest.spine ? pubState.ebookManifest.spine.map((s, i) => ({ title: "Capitolo " + (i + 1), href: s })) : []);

  navList.forEach((item, idx) => {
    let spineIdx = idx;
    if (pubState.ebookManifest && pubState.ebookManifest.spine) {
      const found = pubState.ebookManifest.spine.findIndex(s => s === item.href || item.href.endsWith(s) || s.endsWith(item.href.split("#")[0]));
      if (found !== -1) spineIdx = found;
    }
    const opt = document.createElement("option");
    opt.value = String(spineIdx);
    opt.textContent = item.title || ("Capitolo " + (idx + 1));
    select.appendChild(opt);
  });
  select.value = String(pubState.ebookCurrentChapterIndex || 0);
}

async function renderPubPdfView() {
  if (!pubState.pdfDoc) return;
  cancelPubPdfRender();
  const currentRenderGen = pubState.renderGeneration;

  const viewportEl = $("pubPdfViewport");
  viewportEl.innerHTML = "";
  viewportEl.classList.toggle("facing-mode", pubState.viewMode === "facing");

  const total = pubState.totalPages;
  let pagesToRender = [];

  if (pubState.viewMode === "single") {
    pagesToRender = [pubState.currentPage];
    $("pubPageIndicator").textContent = t("pub_page_indicator", { p: pubState.currentPage, total: total });
  } else {
    // Facing pages: page 1 alone on the right (recto), then spreads (verso on left, recto on right)
    if (pubState.currentPage === 1) {
      pagesToRender = [1];
      $("pubPageIndicator").textContent = t("pub_page_indicator", { p: 1, total: total });
    } else {
      const verso = (pubState.currentPage % 2 === 0) ? pubState.currentPage : pubState.currentPage - 1;
      const recto = verso + 1;
      pagesToRender = [verso];
      if (recto <= total) {
        pagesToRender.push(recto);
        $("pubPageIndicator").textContent = `${t("pub_page_indicator", { p: verso, total: total })} & ${recto}`;
      } else {
        $("pubPageIndicator").textContent = t("pub_page_indicator", { p: verso, total: total });
      }
    }
  }

  $("pubJumpChapter").value = String(pubState.currentPage);
  $("btnPubPrevPage").disabled = (pubState.currentPage <= 1);
  $("btnPubNextPage").disabled = (pubState.currentPage >= total);

  // Calcola dimensione zoom
  const containerWidth = $("pubViewerContainer").clientWidth || 800;
  const containerHeight = $("pubViewerContainer").clientHeight || 600;

  // Se siamo in modalità affiancata e mostriamo la prima pagina (Recto), inseriamo prima il placeholder Verso a sinistra
  if (pubState.viewMode === "facing" && pagesToRender[0] === 1) {
    const p1 = await pubState.pdfDoc.getPage(1);
    if (currentRenderGen !== pubState.renderGeneration) return;
    const baseVp = p1.getViewport({ scale: 1.0 });
    let pScale = 1.0;
    if (pubState.zoom === "fit-width") {
      const availW = (containerWidth - 64 - 24) / 2;
      const targetW = availW - 18;
      pScale = Math.max(0.2, Math.min(3.0, targetW / baseVp.width));
    } else if (pubState.zoom === "fit-page") {
      const availH = containerHeight - 120;
      pScale = Math.max(0.2, Math.min(3.0, (availH - 40) / baseVp.height));
    } else {
      pScale = parseFloat(pubState.zoom) || 1.0;
    }
    const scaledVp = p1.getViewport({ scale: pScale });
    const phCard = document.createElement("div");
    phCard.className = "pub-page-placeholder";
    phCard.style.width = Math.floor(scaledVp.width + 18) + "px";
    phCard.style.height = Math.floor(scaledVp.height + 40) + "px";
    viewportEl.appendChild(phCard);
  }

  for (const pageNum of pagesToRender) {
    const page = await pubState.pdfDoc.getPage(pageNum);
    if (currentRenderGen !== pubState.renderGeneration) return;
    const baseViewport = page.getViewport({ scale: 1.0 });

    let scale = 1.0;
    if (pubState.zoom === "fit-width") {
      const availW = pubState.viewMode === "facing" ? ((containerWidth - 64 - 24) / 2) : (containerWidth - 48);
      const targetW = availW - 18;
      scale = Math.max(0.2, Math.min(3.0, targetW / baseViewport.width));
    } else if (pubState.zoom === "fit-page") {
      const availH = containerHeight - 120;
      scale = Math.max(0.2, Math.min(3.0, (availH - 40) / baseViewport.height));
    } else {
      scale = parseFloat(pubState.zoom) || 1.0;
    }

    const scaledViewport = page.getViewport({ scale: scale });
    const isRecto = (pageNum % 2 === 1);

    const card = document.createElement("div");
    card.className = "pub-page-card";
    card.id = `pub-card-page-${pageNum}`;

    const cardHeader = document.createElement("div");
    cardHeader.className = "pub-page-card-header";
    cardHeader.innerHTML = `<span>${t("pub_page_indicator", { p: pageNum, total: total })}</span><span>${isRecto ? t("pub_recto") : t("pub_verso")}</span>`;
    card.appendChild(cardHeader);

    const wrap = document.createElement("div");
    wrap.className = "pub-page-canvas-wrap";
    wrap.style.width = Math.floor(scaledViewport.width) + "px";
    wrap.style.height = Math.floor(scaledViewport.height) + "px";

    const canvas = document.createElement("canvas");
    canvas.className = "pub-page-canvas";
    const dpr = window.devicePixelRatio || 1;
    canvas.width = Math.floor(scaledViewport.width * dpr);
    canvas.height = Math.floor(scaledViewport.height * dpr);
    canvas.style.width = Math.floor(scaledViewport.width) + "px";
    canvas.style.height = Math.floor(scaledViewport.height) + "px";

    const ctx = canvas.getContext("2d");
    ctx.scale(dpr, dpr);

    wrap.appendChild(canvas);

    // Guide overlay SVG con scala precisa e dati reali dell'artefatto
    if (pubState.guides.cut || pubState.guides.safe) {
      const artPages = (pubState.printArtifact && pubState.printArtifact.page_count) || total || 200;
      const artBleed = Boolean(pubState.printArtifact && pubState.printArtifact.settings && pubState.printArtifact.settings.bleed);
      const svg = createPubGuidesSVG(scaledViewport.width, scaledViewport.height, isRecto, artBleed, scale, artPages);
      wrap.appendChild(svg);
    }

    card.appendChild(wrap);
    viewportEl.appendChild(card);

    const renderTask = page.render({ canvasContext: ctx, viewport: scaledViewport });
    pubState.renderTasks.push(renderTask);
    try {
      await renderTask.promise;
    } catch (_) {}
    if (currentRenderGen !== pubState.renderGeneration) return;
  }
}

function kdpGutterInches(pages) {
  if (pages <= 150) return 0.375;
  if (pages <= 300) return 0.5;
  if (pages <= 500) return 0.625;
  if (pages <= 700) return 0.75;
  if (pages <= 800) return 0.875;
  return 1.0;
}

function createPubGuidesSVG(w, h, isRecto, hasBleed, scale = 1.0, pageCount = 200) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "pub-guides-overlay");
  svg.setAttribute("viewBox", `0 0 ${w} ${h}`);
  svg.style.width = w + "px";
  svg.style.height = h + "px";

  // Calcolo scala effettiva rispetto alla dimensione base (432pt per 6in)
  const effScale = (arguments.length >= 5 && scale > 0) ? scale : (w / 432 || 1);
  const dpi = 72 * effScale;
  const bleedOffset = hasBleed ? 0.125 * dpi : 0;

  // Print pages have bleed only on the outer edge, never on the binding edge.
  const trimX = isRecto ? 0 : bleedOffset;
  const trimY = bleedOffset;
  const trimW = w - bleedOffset;
  const trimH = h - 2 * bleedOffset;

  // Guida di taglio (Trim guide)
  if (pubState.guides.cut) {
    const cutRect = document.createElementNS("http://www.w3.org/2000/svg", "rect");
    cutRect.setAttribute("x", trimX);
    cutRect.setAttribute("y", trimY);
    cutRect.setAttribute("width", trimW);
    cutRect.setAttribute("height", trimH);
    cutRect.setAttribute("fill", "none");
    cutRect.setAttribute("stroke", "#e74c3c");
    cutRect.setAttribute("stroke-width", String(Math.max(1, 1.5 * effScale)));
    cutRect.setAttribute("stroke-dasharray", `${4 * effScale},${4 * effScale}`);
    svg.appendChild(cutRect);
  }

  // Guida Area Sicura (Safe Area)
  if (pubState.guides.safe) {
    const safeMargin = 0.25 * dpi;
    const gutterMargin = (arguments.length >= 6 ? kdpGutterInches(pageCount) : 0.375) * dpi;
    const safeX = trimX + (isRecto ? gutterMargin : safeMargin);
    const safeY = trimY + safeMargin;
    const safeW = trimW - (safeMargin + gutterMargin);
    const safeH = trimH - 2 * safeMargin;

    const safeRect = document.createElementNS("http://www.w3.org/2000/svg", "rect");
    safeRect.setAttribute("x", safeX);
    safeRect.setAttribute("y", safeY);
    safeRect.setAttribute("width", Math.max(10, safeW));
    safeRect.setAttribute("height", Math.max(10, safeH));
    safeRect.setAttribute("fill", "rgba(52, 152, 219, 0.05)");
    safeRect.setAttribute("stroke", "#3498db");
    safeRect.setAttribute("stroke-width", String(Math.max(1, 1.5 * effScale)));
    safeRect.setAttribute("stroke-dasharray", `${3 * effScale},${3 * effScale}`);
    svg.appendChild(safeRect);
  }

  return svg;
}

function renderPubEbookView() {
  const frame = $("pubEbookDeviceFrame");
  frame.className = `pub-device-frame profile-${pubState.ebookProfile} orient-${pubState.ebookOrient}`;
  const iframe = $("pubEbookFrame");

  if (pubState.ebookArtifact && pubState.ebookManifest && pubState.ebookManifest.spine && pubState.ebookManifest.spine.length) {
    const spine = pubState.ebookManifest.spine;
    const idx = Math.max(0, Math.min(pubState.ebookCurrentChapterIndex, spine.length - 1));
    pubState.ebookCurrentChapterIndex = idx;
    const chHref = spine[idx];
    const targetSrc = "/api/publication-preview/" + pubState.ebookArtifact.preview_id + "/epub/" + chHref;

    if (iframe.src !== targetSrc && !iframe.src.endsWith(targetSrc)) {
      iframe.src = targetSrc;
      iframe.onload = () => {
        applyEbookFontSize();
        try {
          iframe.contentDocument?.addEventListener("keydown", (e) => {
            if (e.key === "Tab") {
              e.preventDefault();
              if (e.shiftKey) {
                $("btnPubFontReset")?.focus();
              } else {
                $("btnPubTargetPrint")?.focus();
              }
            }
          });
        } catch (_) {}
      };
    } else {
      applyEbookFontSize();
    }

    const navItem = pubState.ebookManifest.nav ? pubState.ebookManifest.nav.find(n => n.href.split("#")[0] === chHref) : null;
    const title = (navItem && navItem.title) || ("Capitolo " + (idx + 1));
    $("pubPageIndicator").textContent = `${title} (${idx + 1} / ${spine.length})`;
    $("pubJumpChapter").value = String(idx);
    $("btnPubPrevPage").disabled = (idx <= 0);
    $("btnPubNextPage").disabled = (idx >= spine.length - 1);
  } else {
    iframe.src = "about:blank";
    $("pubPageIndicator").textContent = "—";
    $("btnPubPrevPage").disabled = true;
    $("btnPubNextPage").disabled = true;
  }
}

// Inizializzazione eventi Anteprima Pubblicazione
$("btnPubPreview")?.addEventListener("click", openPublicationPreviewModal);
$("btnPubPreviewClose")?.addEventListener("click", closePublicationPreviewModal);
$("btnPubTargetPrint")?.addEventListener("click", () => setPublicationTarget("print"));
$("btnPubTargetEbook")?.addEventListener("click", () => setPublicationTarget("ebook"));
$("btnPubUpdate")?.addEventListener("click", updatePublicationPreview);
$("btnPubRetry")?.addEventListener("click", updatePublicationPreview);

$("pubTrim")?.addEventListener("change", (e) => {
  pubState.printSettings.trim = e.target.value;
  if (pubState.printStatus === "current") {
    pubState.printStatus = "stale";
    updatePubStatusUI();
  }
});
$("pubBleed")?.addEventListener("change", (e) => {
  pubState.printSettings.bleed = e.target.checked;
  if (pubState.printStatus === "current") {
    pubState.printStatus = "stale";
    updatePubStatusUI();
  }
  if (pubState.printArtifact) {
    renderPubPdfView();
  }
});

$("btnPubViewSingle")?.addEventListener("click", () => {
  pubState.viewMode = "single";
  $("btnPubViewSingle").classList.add("active");
  $("btnPubViewFacing").classList.remove("active");
  renderPubPdfView();
});
$("btnPubViewFacing")?.addEventListener("click", () => {
  pubState.viewMode = "facing";
  $("btnPubViewFacing").classList.add("active");
  $("btnPubViewSingle").classList.remove("active");
  renderPubPdfView();
});

$("pubGuideCut")?.addEventListener("change", (e) => {
  pubState.guides.cut = e.target.checked;
  renderPubPdfView();
});
$("pubGuideSafe")?.addEventListener("change", (e) => {
  pubState.guides.safe = e.target.checked;
  renderPubPdfView();
});

$("btnPubProfileReader")?.addEventListener("click", () => {
  pubState.ebookProfile = "reader";
  $("btnPubProfileReader").classList.add("active");
  $("btnPubProfileTablet").classList.remove("active");
  $("btnPubProfilePhone").classList.remove("active");
  renderPubEbookView();
});
$("btnPubProfileTablet")?.addEventListener("click", () => {
  pubState.ebookProfile = "tablet";
  $("btnPubProfileReader").classList.remove("active");
  $("btnPubProfileTablet").classList.add("active");
  $("btnPubProfilePhone").classList.remove("active");
  renderPubEbookView();
});
$("btnPubProfilePhone")?.addEventListener("click", () => {
  pubState.ebookProfile = "phone";
  $("btnPubProfileReader").classList.remove("active");
  $("btnPubProfileTablet").classList.remove("active");
  $("btnPubProfilePhone").classList.add("active");
  renderPubEbookView();
});

$("btnPubOrientPortrait")?.addEventListener("click", () => {
  pubState.ebookOrient = "portrait";
  $("btnPubOrientPortrait").classList.add("active");
  $("btnPubOrientLandscape").classList.remove("active");
  renderPubEbookView();
});
$("btnPubOrientLandscape")?.addEventListener("click", () => {
  pubState.ebookOrient = "landscape";
  $("btnPubOrientLandscape").classList.add("active");
  $("btnPubOrientPortrait").classList.remove("active");
  renderPubEbookView();
});

function applyEbookFontSize() {
  $("pubFontVal").textContent = pubState.ebookFontSize + "%";
  try {
    const doc = $("pubEbookFrame").contentDocument;
    if (doc && doc.documentElement) {
      doc.documentElement.style.fontSize = pubState.ebookFontSize + "%";
    }
  } catch (_) {}
}
$("btnPubFontDec")?.addEventListener("click", () => {
  pubState.ebookFontSize = Math.max(70, pubState.ebookFontSize - 10);
  applyEbookFontSize();
});
$("btnPubFontInc")?.addEventListener("click", () => {
  pubState.ebookFontSize = Math.min(200, pubState.ebookFontSize + 10);
  applyEbookFontSize();
});
$("btnPubFontReset")?.addEventListener("click", () => {
  pubState.ebookFontSize = 100;
  applyEbookFontSize();
});

$("btnPubPrevPage")?.addEventListener("click", () => {
  if (pubState.target === "print") {
    const step = (pubState.viewMode === "facing") ? 2 : 1;
    pubState.currentPage = Math.max(1, pubState.currentPage - step);
    renderPubPdfView();
  } else {
    pubState.ebookCurrentChapterIndex = Math.max(0, pubState.ebookCurrentChapterIndex - 1);
    renderPubEbookView();
  }
});
$("btnPubNextPage")?.addEventListener("click", () => {
  if (pubState.target === "print") {
    const step = (pubState.viewMode === "facing") ? 2 : 1;
    pubState.currentPage = Math.min(pubState.totalPages, pubState.currentPage + step);
    renderPubPdfView();
  } else {
    const spine = (pubState.ebookManifest && pubState.ebookManifest.spine) || [];
    pubState.ebookCurrentChapterIndex = Math.min(spine.length - 1, pubState.ebookCurrentChapterIndex + 1);
    renderPubEbookView();
  }
});
$("pubJumpChapter")?.addEventListener("change", (e) => {
  const p = parseInt(e.target.value, 10);
  if (!isNaN(p)) {
    if (pubState.target === "print") {
      pubState.currentPage = p;
      renderPubPdfView();
    } else {
      pubState.ebookCurrentChapterIndex = p;
      renderPubEbookView();
    }
  }
});

$("pubZoomSelect")?.addEventListener("change", (e) => {
  pubState.zoom = e.target.value;
  renderPubPdfView();
});
$("btnPubZoomOut")?.addEventListener("click", () => {
  const steps = ["0.5", "0.75", "1.0", "1.25", "1.5", "2.0"];
  let cur = parseFloat(pubState.zoom) || 1.0;
  for (let i = steps.length - 1; i >= 0; i--) {
    if (parseFloat(steps[i]) < cur) {
      pubState.zoom = steps[i];
      $("pubZoomSelect").value = steps[i];
      renderPubPdfView();
      return;
    }
  }
});
$("btnPubZoomIn")?.addEventListener("click", () => {
  const steps = ["0.5", "0.75", "1.0", "1.25", "1.5", "2.0"];
  let cur = parseFloat(pubState.zoom) || 1.0;
  for (let i = 0; i < steps.length; i++) {
    if (parseFloat(steps[i]) > cur) {
      pubState.zoom = steps[i];
      $("pubZoomSelect").value = steps[i];
      renderPubPdfView();
      return;
    }
  }
});

// Scorciatoie da tastiera globali (Focus mode, Escape, ecc.)
document.addEventListener("keydown", (e) => {
  // Modalità non disturbo: F11 o Cmd+Shift+F / Ctrl+Shift+F
  if (e.key === "F11" || ((e.metaKey || e.ctrlKey) && e.shiftKey && (e.key === "F" || e.key === "f"))) {
    if (state.book && currentFile) {
      e.preventDefault();
      toggleFocusMode();
      return;
    }
  }

  if (e.key === "Escape") {
    if ($("pubPreviewModal") && !$("pubPreviewModal").classList.contains("hidden")) {
      closePublicationPreviewModal();
      return;
    } else if ($("tutorialModal") && !$("tutorialModal").classList.contains("hidden")) {
      hideTutorialModal();
      return;
    } else if ($("reportIssueModal") && !$("reportIssueModal").classList.contains("hidden")) {
      hideReportIssueModal();
      return;
    } else if ($("tableModal") && !$("tableModal").classList.contains("hidden")) {
      hideTableModal();
      return;
    } else if (isFocusMode) {
      e.preventDefault();
      exitFocusMode();
      return;
    }
  }
});

document.addEventListener("mousemove", () => {
  if (isFocusMode) pokeFocusHud();
});

/* -------------------------------------- Welcome Launcher & Recent Projects */


function shortenPath(p) {
  if (!p) return "—";
  const home = (state && state.homeDir) || "";
  if (home && p.startsWith(home)) {
    return "~" + p.slice(home.length);
  }
  return p;
}

function formatRecentDate(iso) {
  if (!iso) return "";
  try {
    const d = new Date(iso);
    const now = new Date();
    const timeStr = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    if (d.toDateString() === now.toDateString()) {
      return (currentLang === "it" ? "Oggi " : "Today ") + timeStr;
    }
    const yesterday = new Date(now);
    yesterday.setDate(now.getDate() - 1);
    if (d.toDateString() === yesterday.toDateString()) {
      return (currentLang === "it" ? "Ieri " : "Yesterday ") + timeStr;
    }
    return d.toLocaleDateString([], { month: "short", day: "numeric", year: "numeric" });
  } catch (_) {
    return "";
  }
}

async function loadRecents() {
  try {
    const res = await fetch("/api/recents").then(r => r.json());
    recentsData = res.recents || [];
    workspaceDir = res.workspace_dir || res.effective_ws || "";
    if ($("welcomeWsPath")) {
      $("welcomeWsPath").textContent = shortenPath(workspaceDir);
      $("welcomeWsPath").title = workspaceDir;
    }
    renderRecentList();
  } catch (err) {
    console.error("Error loading recents:", err);
  }
}

function renderRecentList() {
  const listEl = $("welcomeRecentList");
  const emptyEl = $("welcomeEmptyRecents");
  const btnOpenSel = $("btnWelcomeOpenSelected");
  const pruneBtn = $("btnPruneMissingRecents");
  if (!listEl) return;

  listEl.innerHTML = "";
  const filter = ($("recentsSearchInput")?.value || "").toLowerCase().trim();

  let items = recentsData;
  if (activeRecentsTab === "workspace" && workspaceDir) {
    const cleanWs = workspaceDir.replace(/\/+$/, "");
    items = items.filter(r => r.path && r.path.startsWith(cleanWs));
  }

  if (filter) {
    items = items.filter(r =>
      (r.title && r.title.toLowerCase().includes(filter)) ||
      (r.author && r.author.toLowerCase().includes(filter)) ||
      (r.path && r.path.toLowerCase().includes(filter))
    );
  }

  const hasMissing = recentsData.some(r => r.exists === false);
  if (pruneBtn) {
    pruneBtn.classList.toggle("hidden", !hasMissing);
  }

  if (!items.length) {
    emptyEl?.classList.remove("hidden");
    if (btnOpenSel) btnOpenSel.disabled = true;
    selectedRecentPath = null;
    return;
  }

  emptyEl?.classList.add("hidden");

  items.forEach(r => {
    const card = document.createElement("div");
    card.className = "recent-card";
    if (r.path === selectedRecentPath) card.classList.add("selected");
    if (r.exists === false) card.classList.add("recent-missing");
    card.setAttribute("tabindex", "0");
    card.setAttribute("role", "option");

    // Thumb Jacket
    const thumb = document.createElement("div");
    const styleName = (r.style || "classic").toLowerCase();
    thumb.className = `recent-thumb-jacket jacket-${styleName}`;

    if (r.has_cover) {
      const img = document.createElement("img");
      img.src = `/api/recents/cover?path=${encodeURIComponent(r.path)}`;
      img.className = "recent-cover-img";
      img.alt = r.title || "Cover";
      img.onerror = () => {
        img.remove();
        thumb.innerHTML = `<div class="recent-jacket-inner"><div class="recent-jacket-title">${esc(r.title || "")}</div><div class="recent-jacket-flower">📖</div></div>`;
      };
      thumb.appendChild(img);
    } else {
      thumb.innerHTML = `<div class="recent-jacket-inner"><div class="recent-jacket-title">${esc(r.title || "")}</div><div class="recent-jacket-flower">📖</div></div>`;
    }

    // Meta
    const meta = document.createElement("div");
    meta.className = "recent-meta";

    const titleDiv = document.createElement("div");
    titleDiv.className = "recent-title";
    titleDiv.textContent = r.title || r.folder_name || t("untitled");

    if (r.exists === false) {
      const missingBadge = document.createElement("span");
      missingBadge.className = "recent-missing-badge";
      missingBadge.textContent = "⚠️ " + t("recent_missing_badge");
      titleDiv.appendChild(missingBadge);
    }

    const catDiv = document.createElement("div");
    catDiv.className = "recent-category";
    const catIcon = document.createElement("span");
    catIcon.textContent = "📁 ";
    const catParent = document.createElement("span");
    catParent.className = "recent-parent";
    catParent.textContent = r.parent_name || "";
    const catSep = document.createElement("span");
    catSep.textContent = " / ";
    const catFname = document.createElement("span");
    catFname.className = "recent-fname";
    catFname.textContent = r.folder_name || "";
    catDiv.append(catIcon, catParent, catSep, catFname);

    const pathDiv = document.createElement("div");
    pathDiv.className = "recent-path";
    pathDiv.textContent = shortenPath(r.path);
    pathDiv.title = r.path;

    const dateDiv = document.createElement("div");
    dateDiv.className = "recent-date";
    dateDiv.textContent = formatRecentDate(r.last_opened);

    meta.append(titleDiv, catDiv, pathDiv, dateDiv);

    // Remove button (sempre visibile e facilmente cliccabile)
    const removeBtn = document.createElement("button");
    removeBtn.type = "button";
    removeBtn.className = "recent-remove-btn";
    removeBtn.innerHTML = "🗑️";
    removeBtn.title = t("recents_remove_tooltip");
    removeBtn.setAttribute("aria-label", t("recent_remove_btn"));
    removeBtn.addEventListener("click", async (e) => {
      e.stopPropagation();
      e.preventDefault();
      await removeRecent(r.path);
    });

    card.append(thumb, meta, removeBtn);

    // Click on card: apre o propone rimozione se la cartella è inesistente
    card.addEventListener("click", () => {
      selectRecentCard(r.path, card);
      if (r.exists === false) {
        showModal(
          t("recent_missing_title"),
          `${t("recent_missing_desc")}\n${r.path}\n\n${t("recent_missing_prompt")}`,
          [
            {
              label: t("recent_remove_btn"),
              danger: true,
              onClick: async () => {
                await removeRecent(r.path);
              }
            },
            { label: t("cancel") }
          ]
        );
        return;
      }
      openRecentProject(r.path);
    });

    card.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        selectRecentCard(r.path, card);
        if (r.exists === false) {
          showModal(
            t("recent_missing_title"),
            `${t("recent_missing_desc")}\n${r.path}\n\n${t("recent_missing_prompt")}`,
            [
              {
                label: t("recent_remove_btn"),
                danger: true,
                onClick: async () => {
                  await removeRecent(r.path);
                }
              },
              { label: t("cancel") }
            ]
          );
          return;
        }
        openRecentProject(r.path);
      }
    });

    listEl.appendChild(card);
  });
}

function selectRecentCard(p, cardEl) {
  selectedRecentPath = p;
  document.querySelectorAll(".recent-card").forEach(c => c.classList.remove("selected"));
  if (cardEl) cardEl.classList.add("selected");
  if ($("btnWelcomeOpenSelected")) $("btnWelcomeOpenSelected").disabled = false;
}

async function removeRecent(path) {
  try {
    await fetch("/api/recents/remove", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path }),
    });
    recentsData = recentsData.filter(r => r.path !== path);
    if (selectedRecentPath === path) {
      selectedRecentPath = null;
      if ($("btnWelcomeOpenSelected")) $("btnWelcomeOpenSelected").disabled = true;
    }
    renderRecentList();
  } catch (err) {
    console.error("Error removing recent:", err);
  }
}

async function pruneMissingRecents() {
  try {
    await fetch("/api/recents/prune", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
    });
    await loadRecents();
  } catch (err) {
    console.error("Error pruning missing recents:", err);
  }
}

async function clearAllRecents() {
  if (!recentsData.length) return;
  showModal(
    t("recents_clear_confirm_title"),
    t("recents_clear_confirm_desc"),
    [
      {
        label: t("recents_btn_clear_all"),
        danger: true,
        onClick: async () => {
          try {
            await fetch("/api/recents/clear", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
            });
            await loadRecents();
          } catch (err) {
            console.error("Error clearing recents:", err);
          }
        }
      },
      { label: t("cancel") }
    ]
  );
}

async function openRecentProject(path) {
  if (isProjectSwitching || isBuilding) return;
  await runProjectTransition(async () => {
    const res = await api("/api/openat", {
      method: "POST",
      body: JSON.stringify({ path }),
    });
    if (res.error) throw new Error(res.error);
    return res;
  });
}

async function closeCurrentBook() {
  if (isProjectSwitching || isBuilding) return;
  if (isFocusMode) exitFocusMode();
  const ok = await runProjectTransition(async () => {
    const res = await api("/api/closebook", { method: "POST" });
    return res;
  });
  if (!ok) return;
  notesData = [];
  currentNoteFile = "";
  currentNoteVault = "";
  noteDirty = false;
  notifyClientDirty();
  resetNoteEditor();
  closeNotebookDrawer();
  await loadRecents();
}

async function changeWorkspace() {
  const dir = await pickPath("dirs", "", t("recents_workspace_label"));
  if (!dir) return;
  const p = typeof dir === "string" ? dir : (dir.path || dir.dir);
  await setAsWorkspace(p);
}

async function setAsWorkspace(dir) {
  try {
    const res = await fetch("/api/workspace", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path: dir }),
    }).then(r => r.json());
    if (res.error) throw new Error(res.error);
    workspaceDir = res.workspace_dir || dir;
    if ($("welcomeWsPath")) {
      $("welcomeWsPath").textContent = shortenPath(workspaceDir);
      $("welcomeWsPath").title = workspaceDir;
    }
    if ($("btnFsSetAsWorkspace")) {
      const origText = $("btnFsSetAsWorkspace").textContent;
      $("btnFsSetAsWorkspace").textContent = "✔ " + t("fs_workspace_saved");
      setTimeout(() => {
        if ($("btnFsSetAsWorkspace")) $("btnFsSetAsWorkspace").textContent = origText;
      }, 1800);
    }
    await loadRecents();
  } catch (err) {
    showModal(t("modal_error"), String(err), [[t("modal_close"), hideModal]]);
  }
}

function showTutorialModal(tab = "guide") {
  activeTutorialTab = tab;
  $("tabTutGuide")?.classList.toggle("active", tab === "guide");
  $("tabTutShortcuts")?.classList.toggle("active", tab === "shortcuts");
  const container = $("tutorialContent");
  if (container && typeof getTutorialHTML === "function") {
    container.innerHTML = getTutorialHTML(currentLang, tab);
  }
  $("tutorialModal")?.classList.remove("hidden");
}

function hideTutorialModal() {
  $("tutorialModal")?.classList.add("hidden");
}

// Event Listeners per Welcome Screen e Recenti
$("btnWelcomeNew")?.addEventListener("click", showNewProjectModal);
$("btnWelcomeDocx")?.addEventListener("click", importDocx);
$("btnWelcomeBrowse")?.addEventListener("click", openVaultFolder);
$("btnWelcomeTutorial")?.addEventListener("click", () => showTutorialModal("guide"));
$("btnWelcomeShortcuts")?.addEventListener("click", () => showTutorialModal("shortcuts"));

$("btnWelcomeChangeWs")?.addEventListener("click", changeWorkspace);
$("btnWelcomeOpenOther")?.addEventListener("click", openVaultFolder);
$("btnWelcomeOpenSelected")?.addEventListener("click", () => {
  if (selectedRecentPath) openRecentProject(selectedRecentPath);
});
$("btnPruneMissingRecents")?.addEventListener("click", pruneMissingRecents);
$("btnClearAllRecents")?.addEventListener("click", clearAllRecents);

$("tabRecentsAll")?.addEventListener("click", () => {
  activeRecentsTab = "all";
  $("tabRecentsAll")?.classList.add("active");
  $("tabRecentsWorkspace")?.classList.remove("active");
  renderRecentList();
});
$("tabRecentsWorkspace")?.addEventListener("click", () => {
  activeRecentsTab = "workspace";
  $("tabRecentsWorkspace")?.classList.add("active");
  $("tabRecentsAll")?.classList.remove("active");
  renderRecentList();
});
$("recentsSearchInput")?.addEventListener("input", renderRecentList);

// Topbar listeners
$("btnTopbarRecents")?.addEventListener("click", () => {
  if (state.vault) {
    closeCurrentBook();
  } else {
    loadRecents();
  }
});
$("btnCloseBook")?.addEventListener("click", closeCurrentBook);
$("btnToggleTheme")?.addEventListener("click", toggleTheme);

// Tutorial modal listeners
$("btnTutorialClose")?.addEventListener("click", hideTutorialModal);
$("btnTutorialDismiss")?.addEventListener("click", hideTutorialModal);
$("tabTutGuide")?.addEventListener("click", () => showTutorialModal("guide"));
$("tabTutShortcuts")?.addEventListener("click", () => showTutorialModal("shortcuts"));
$("tutorialModal")?.addEventListener("click", (e) => {
  if (e.target === $("tutorialModal")) hideTutorialModal();
});

// File browser quick buttons listeners
$("btnFsWorkspace")?.addEventListener("click", () => fsBrowse(workspaceDir || (state && state.docsDir) || "~"));
$("btnFsDocs")?.addEventListener("click", () => fsBrowse((state && state.docsDir) || "~/Documents"));
$("btnFsDesktop")?.addEventListener("click", () => fsBrowse((state && state.desktopDir) || "~/Desktop"));
$("btnFsDownloads")?.addEventListener("click", () => fsBrowse((state && state.downloadsDir) || "~/Downloads"));
$("btnFsHome")?.addEventListener("click", () => fsBrowse("~"));
$("btnFsVolumes")?.addEventListener("click", () => fsBrowse("/"));
$("btnFsSetAsWorkspace")?.addEventListener("click", () => {
  if (fsState && fsState.cur) setAsWorkspace(fsState.cur);
});
$("fsFilterInput")?.addEventListener("input", () => renderFsList(fsLastItems));

// Modal Segnala un problema / Bug Report (MCP & Diagnostica)
let currentReportId = "";

async function openReportIssueModal() {
  if ($("reportDesc")) $("reportDesc").value = "";
  if ($("reportExpected")) $("reportExpected").value = "";
  if ($("reportObserved")) $("reportObserved").value = "";
  currentReportId = "";
  $("reportPreviewArea")?.classList.add("hidden");
  $("btnReportCopy")?.classList.add("hidden");
  $("btnReportExport")?.classList.add("hidden");
  if ($("reportStatusBadge")) $("reportStatusBadge").textContent = "";

  $("reportIssueModal")?.classList.remove("hidden");
  $("reportDesc")?.focus();

  const listEl = $("reportErrorsList");
  if (listEl) {
    listEl.innerHTML = '<span class="muted">' + esc(t("loading") || "Caricamento...") + '</span>';
    try {
      const data = await fetch("/api/diagnostics/events").then(r => r.json()).catch(() => ({}));
      const events = Array.isArray(data.events) ? data.events : (Array.isArray(data) ? data : []);
      listEl.innerHTML = "";
      if (events.length > 0) {
        events.slice(0, 20).forEach(ev => {
          const row = document.createElement("label");
          row.style.cssText = "display:flex;align-items:flex-start;gap:8px;margin:4px 0;cursor:pointer;";
          const cb = document.createElement("input");
          cb.type = "checkbox";
          cb.value = ev.error_id || ev.id || "";
          cb.checked = true;
          cb.style.marginTop = "2px";
          const desc = document.createElement("div");
          desc.style.lineHeight = "1.3";
          const lvl = (ev.severity || ev.level || "ERROR").toUpperCase();
          const op = ev.operation ? `[${ev.operation}] ` : "";
          const cd = ev.code ? `(${ev.code}) ` : "";
          desc.innerHTML = `<span style="font-weight:bold;color:${lvl === 'ERROR' ? '#ff6b6b' : '#f0a500'};">${lvl}</span> <span>${op}${cd}${esc(ev.message || '')}</span>`;
          row.append(cb, desc);
          listEl.appendChild(row);
        });
      } else {
        listEl.innerHTML = '<span class="muted" style="font-size:12px;">' + esc(t("report_no_recent_errors")) + '</span>';
      }
    } catch {
      listEl.innerHTML = '<span class="muted" style="font-size:12px;">' + esc(t("report_no_recent_errors")) + '</span>';
    }
  }
}

function hideReportIssueModal() {
  $("reportIssueModal")?.classList.add("hidden");
}

async function prepareIssueReport() {
  const desc = ($("reportDesc")?.value || "").trim();
  if (!desc) {
    alert(t("report_desc_label") || "Descrivi cosa stavi facendo o cosa non funziona.");
    $("reportDesc")?.focus();
    return false;
  }
  const expected = ($("reportExpected")?.value || "").trim();
  const observed = ($("reportObserved")?.value || "").trim();
  const checkboxes = document.querySelectorAll("#reportErrorsList input[type='checkbox']:checked");
  const eventIds = Array.from(checkboxes).map(cb => cb.value).filter(Boolean);

  $("btnReportPrepare").disabled = true;
  try {
    const res = await fetch("/api/diagnostics/report", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        description: desc,
        expected,
        observed,
        error_ids: eventIds,
      }),
    }).then(r => r.json());

    if (res.error) throw new Error(res.error);

    currentReportId = res.report_id || "";
    if ($("reportPreviewText")) $("reportPreviewText").value = res.markdown_content || "";
    $("reportPreviewArea")?.classList.remove("hidden");
    $("btnReportCopy")?.classList.remove("hidden");
    $("btnReportExport")?.classList.remove("hidden");
    if ($("reportStatusBadge")) $("reportStatusBadge").textContent = "";
    return true;
  } catch (e) {
    alert("Errore generazione rapporto: " + (e.message || String(e)));
    return false;
  } finally {
    $("btnReportPrepare").disabled = false;
  }
}

async function copyIssueReport() {
  const text = $("reportPreviewText")?.value;
  if (!text) return;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
    } else {
      $("reportPreviewText").select();
      document.execCommand("copy");
    }
    const badge = $("reportStatusBadge");
    if (badge) {
      badge.textContent = t("report_copied_msg") || "Copiato!";
      setTimeout(() => { if (badge) badge.textContent = ""; }, 3000);
    }
  } catch (e) {
    alert("Copia non riuscita: " + String(e));
  }
}

async function exportIssueReport(overwrite = false) {
  const allowOverwrite = (overwrite === true);
  if (!currentReportId) {
    const ok = await prepareIssueReport();
    if (!ok || !currentReportId) return;
  }

  $("btnReportExport").disabled = true;
  try {
    const res = await fetch("/api/diagnostics/export", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        report_id: currentReportId,
        destination: "",
        overwrite: allowOverwrite,
      }),
    }).then(r => r.json());

    if (res.error) {
      if (res.error.includes("OUTPUT_EXISTS")) {
        showModal("File già esistente", res.error + "\n\nVuoi sovrascrivere il file esistente?", [
          ["Sovrascrivi", async () => { hideModal(); await exportIssueReport(true); }],
          ["Annulla", hideModal]
        ]);
        return;
      }
      throw new Error(res.error);
    }
    showModal("Rapporto Esportato", "Il file Markdown è stato salvato con successo in:\n\n" + res.path, [["OK", hideModal]]);
  } catch (e) {
    alert("Esportazione fallita: " + (e.message || String(e)));
  } finally {
    $("btnReportExport").disabled = false;
  }
}

$("btnReportIssue")?.addEventListener("click", openReportIssueModal);
$("btnReportCloseX")?.addEventListener("click", hideReportIssueModal);
$("btnReportCancel")?.addEventListener("click", hideReportIssueModal);
$("btnReportPrepare")?.addEventListener("click", prepareIssueReport);
$("btnReportCopy")?.addEventListener("click", copyIssueReport);
$("btnReportExport")?.addEventListener("click", () => exportIssueReport(false));
$("reportIssueModal")?.addEventListener("click", (e) => {
  if (e.target === $("reportIssueModal")) hideReportIssueModal();
});

// Trap del focus nelle finestre modali (B14)
document.addEventListener("keydown", (e) => {
  if (e.key !== "Tab") return;
  const activeModal = document.querySelector("#pubPreviewModal:not(.hidden), #modal:not(.hidden), #tutorialModal:not(.hidden), #printModal:not(.hidden), #reportIssueModal:not(.hidden), #fsModal:not(.hidden), #tableModal:not(.hidden)");
  if (!activeModal) return;

  const candidateElements = activeModal.querySelectorAll(
    'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
  );
  const focusable = Array.from(candidateElements).filter((el) => {
    return (
      el.offsetWidth > 0 &&
      el.offsetHeight > 0 &&
      !el.closest(".hidden") &&
      getComputedStyle(el).visibility !== "hidden" &&
      getComputedStyle(el).display !== "none"
    );
  });

  if (focusable.length === 0) {
    e.preventDefault();
    return;
  }

  const idx = focusable.indexOf(document.activeElement);
  if (e.shiftKey) {
    if (idx <= 0) {
      e.preventDefault();
      focusable[focusable.length - 1].focus();
    } else {
      e.preventDefault();
      focusable[idx - 1].focus();
    }
  } else {
    if (idx === -1 || idx >= focusable.length - 1) {
      e.preventDefault();
      focusable[0].focus();
    } else {
      e.preventDefault();
      focusable[idx + 1].focus();
    }
  }
});


initTheme();

refreshState().then(() => {
  initTheme();
  loadCovers();
  initStyleMenu();
  initOpeningMenu();
  initTopbarTitleEditing();
  initNotebook();
  if (state.book && state.book.chapters.length) {
    openChapter(state.book.chapters[0].file);
    setView("split");
  }
});
