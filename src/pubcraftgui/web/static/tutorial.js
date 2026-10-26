// tutorial.js - Guida, tutorial e scorciatoie per PubCraft in 10 lingue

const TUTORIAL_DATA = {
  en: {
    heroTitle: "Welcome to PubCraft",
    heroDesc: "PubCraft is an artisanal book typography and publication studio. It compiles your Markdown manuscripts into real Kindle EPUBs and publication-grade print PDFs with uncompromising fidelity.",
    c1Title: "1. Manuscript & Project Vault",
    c1Desc: "Each book lives in a dedicated folder (vault) containing book.yaml, your chapter Markdown files (.md), a cover/ directory for book covers, and images/ for illustrations.",
    c2Title: "2. Typographic Styles & Ornaments",
    c2Desc: "Choose from 4 tailored styles: Classic (literary fiction), Poetry (verse and drama), Modern (clean essays and non-fiction), and Adelphi (traditional European publishing). Chapter openings feature real drop caps or small caps.",
    c3Title: "3. Writing & Image Pasting",
    c3Desc: "Format headings, quotes, and lists with the minimalist toolbar or native shortcuts. Paste screenshots and images directly from your clipboard (Cmd+V) to automatically store them in images/ and link them in Markdown.",
    c4Title: "4. Real Publication Preview",
    c4Desc: "The Publication Preview compiles real EPUB and PDF byte streams. PDF preview renders exact pages with trim sizes (5x8, 6x9, etc.), gutters, and page counts. Ebook preview displays reader, tablet, and smartphone simulations.",
    c5Title: "5. Workspaces & 1-Click Opening",
    c5Desc: "Set your default writing folder as a Workspace. Your recent projects stay on the Welcome Launcher so you can jump right back into your books with a single click.",
    colAction: "Action",
    colShortcut: "Shortcut",
    colDesc: "Description",
    s1: "Bold",
    s1d: "Toggle **bold** on selected text",
    s2: "Italic",
    s2d: "Toggle *italic* on selected text",
    s3: "Link",
    s3d: "Insert markdown [text](url) link",
    s4: "Save Now",
    s4d: "Flush changes immediately to disk (auto-save is also active)",
    s5: "Paste Image",
    s5d: "Paste screenshot or image directly into chapter and images/",
    s6: "Undo",
    s6d: "Revert last editing change",
    s7: "Redo",
    s7d: "Reapply reverted change",
    s8: "Close Modal",
    s8d: "Dismiss dialogs, file picker, and publication preview",
    s9: "Focus Mode",
    s9d: "Toggle full-screen distraction-free typewriter mode",
    s10: "Toggle Sidebar",
    s10d: "Show or hide the left navigation sidebar",
    s11: "Command Palette",
    s11d: "Search chapters, notes, and actions instantly"
  },
  it: {
    heroTitle: "Benvenuto in PubCraft",
    heroDesc: "PubCraft è uno studio artigianale di impaginazione e pubblicazione editoriale. Trasforma i tuoi manoscritti Markdown in veri EPUB per Kindle e PDF pronti per la stampa con fedeltà tipografica assoluta.",
    c1Title: "1. Manoscritto e Cartella Vault",
    c1Desc: "Ogni libro risiede in una cartella dedicata contenente book.yaml, i file Markdown (.md) dei capitoli, la sottocartella cover/ per le copertine e images/ per le illustrazioni.",
    c2Title: "2. Stili tipografici e Fregi",
    c2Desc: "Scegli tra 4 stili: Classic (narrativa letteraria), Poetry (poesia e teatro), Modern (saggistica contemporanea) e Adelphi (tradizione editoriale classica). L'inizio capitolo supporta capilettera Drop Cap o Small Caps.",
    c3Title: "3. Scrittura e Incolla Immagini",
    c3Desc: "Formatta titoli, citazioni ed elenchi con la barra strumenti o con le scorciatoie. Incolla screenshot e immagini direttamente dagli appunti (Cmd+V) per salvarle automaticamente in images/ e collegarle nel testo.",
    c4Title: "4. Anteprima di Pubblicazione Reale",
    c4Desc: "L'anteprima compila i veri byte di EPUB e PDF. Il visualizzatore PDF mostra le pagine reali con formati di stampa (5x8, 6x9, ecc.), margini interni (gutter) e pagine affiancate. L'ebook simula e-reader, tablet e smartphone.",
    c5Title: "5. Area di Lavoro e Apertura in 1 Clic",
    c5Desc: "Imposta la tua cartella di lavoro principale. Tutti i tuoi progetti recenti restano sempre in mostra nella schermata iniziale per farti entrare subito nel tuo libro con un singolo clic.",
    colAction: "Azione",
    colShortcut: "Scorciatoia",
    colDesc: "Descrizione",
    s1: "Grassetto",
    s1d: "Attiva o disattiva il **grassetto** sul testo selezionato",
    s2: "Corsivo",
    s2d: "Attiva o disattiva il *corsivo* sul testo selezionato",
    s3: "Inserisci Link",
    s3d: "Inserisce collegamento markdown [testo](url)",
    s4: "Salva subito",
    s4d: "Forza il salvataggio immediato su disco (il salvataggio automatico è sempre attivo)",
    s5: "Incolla Immagine",
    s5d: "Incolla un'immagine o screenshot dagli appunti salvandola in images/",
    s6: "Annulla (Undo)",
    s6d: "Annulla l'ultima modifica",
    s7: "Ripeti (Redo)",
    s7d: "Ripristina l'ultima modifica annullata",
    s8: "Chiudi Finestra",
    s8d: "Chiude finestre modali, selettore cartelle e anteprima",
    s9: "Modalità Non Disturbo",
    s9d: "Attiva la scrittura a tutto schermo senza distrazioni (layout macchina da scrivere)",
    s10: "Barra Laterale",
    s10d: "Mostra o nascondi la barra laterale sinistra (stile Obsidian)",
    s11: "Command Palette",
    s11d: "Cerca e salta a capitoli, note o comandi rapidi"
  },
  es: {
    heroTitle: "Bienvenido a PubCraft",
    heroDesc: "PubCraft es un estudio de tipografía y publicación editorial. Convierte tus manuscritos Markdown en auténticos EPUB para Kindle y PDF de imprenta con fidelidad tipográfica absoluta.",
    c1Title: "1. Manuscrito y Carpeta Vault",
    c1Desc: "Cada libro se aloja en una carpeta con book.yaml, archivos Markdown (.md), una carpeta cover/ para portadas e images/ para ilustraciones.",
    c2Title: "2. Estilos Tipográficos y Adornos",
    c2Desc: "4 estilos a tu disposición: Classic (ficción literaria), Poetry (poesía y teatro), Modern (ensayos y no ficción) y Adelphi (tradición clásica). Incluye capitulares (Drop Cap) y versalitas.",
    c3Title: "3. Escritura y Pegado de Imágenes",
    c3Desc: "Formatea con la barra minimalista o atajos rápidos. Pega capturas e imágenes directamente del portapapeles (Cmd+V) para guardarlas en images/ y enlazarlas en Markdown.",
    c4Title: "4. Vista Previa Real de Publicación",
    c4Desc: "Compila bytes reales de EPUB y PDF. Muestra páginas exactas de imprenta (5x8, 6x9, etc.), márgenes y páginas encaradas. El lector de ebook simula e-reader, tableta y móvil.",
    c5Title: "5. Espacio de Trabajo y Apertura en 1 Clic",
    c5Desc: "Define tu carpeta de trabajo. Tus libros recientes permanecen en la pantalla de inicio para entrar inmediatamente con un solo clic.",
    colAction: "Acción",
    colShortcut: "Atajo",
    colDesc: "Descripción",
    s1: "Negrita",
    s1d: "Alterna **negrita** en el texto seleccionado",
    s2: "Cursiva",
    s2d: "Alterna *cursiva* en el texto seleccionado",
    s3: "Enlace",
    s3d: "Inserta enlace markdown [texto](url)",
    s4: "Guardar",
    s4d: "Guarda cambios en disco al instante (el autoguardado está activo)",
    s5: "Pegar Imagen",
    s5d: "Pega una imagen del portapapeles directamente en images/",
    s6: "Deshacer",
    s6d: "Revierte la última modificación",
    s7: "Rehacer",
    s7d: "Reaplica el cambio revertido",
    s8: "Cerrar",
    s8d: "Cierra modales, selector de carpetas y vista previa"
  },
  fr: {
    heroTitle: "Bienvenue sur PubCraft",
    heroDesc: "PubCraft est un studio artisanal de typographie et de publication. Il compile vos manuscrits Markdown en véritables EPUB Kindle et PDF d'impression professionnelle.",
    c1Title: "1. Manuscrit et Dossier Vault",
    c1Desc: "Chaque livre vit dans un dossier dédié comprenant book.yaml, vos fichiers Markdown (.md), le dossier cover/ pour les couvertures et images/ pour les illustrations.",
    c2Title: "2. Styles Typographiques et Lettrines",
    c2Desc: "4 styles au choix : Classic (fiction littéraire), Poetry (poésie et théâtre), Modern (essais et non-fiction) et Adelphi (tradition éditoriale). Lettrines et petites capitales incluses.",
    c3Title: "3. Rédaction et Collage d'Images",
    c3Desc: "Formatez avec la barre d'outils ou les raccourcis clavier. Collez des images directement depuis le presse-papiers (Cmd+V) pour les stocker dans images/ et les lier en Markdown.",
    c4Title: "4. Aperçu Réel de Publication",
    c4Desc: "Compile le flux d'octets réel EPUB et PDF. Le lecteur PDF affiche les pages réelles avec formats de coupe (5x8, 6x9, etc.), marges et pages en vis-à-vis.",
    c5Title: "5. Espace de Travail et Accès en 1 Clic",
    c5Desc: "Définissez votre dossier de travail par défaut. Vos projets récents restent visibles sur l'écran d'accueil pour réouvrir vos livres en 1 clic.",
    colAction: "Action",
    colShortcut: "Raccourci",
    colDesc: "Description",
    s1: "Gras",
    s1d: "Bascule le **gras** sur la sélection",
    s2: "Italique",
    s2d: "Bascule l'*italique* sur la sélection",
    s3: "Lien",
    s3d: "Insère un lien [texte](url)",
    s4: "Enregistrer",
    s4d: "Enregistre immédiatement (l'enregistrement auto est actif)",
    s5: "Coller Image",
    s5d: "Colle une image du presse-papiers dans images/",
    s6: "Annuler",
    s6d: "Annule la dernière frappe",
    s7: "Rétablir",
    s7d: "Rétablit l'action annulée",
    s8: "Fermer",
    s8d: "Ferme les fenêtres modales et l'aperçu"
  },
  de: {
    heroTitle: "Willkommen bei PubCraft",
    heroDesc: "PubCraft ist ein typografisches Buch- und Publikationsstudio. Es kompiliert Ihre Markdown-Manuskripte in echte Kindle-EPUBs und druckreife Druck-PDFs mit höchster typografischer Präzision.",
    c1Title: "1. Manuskript und Vault-Ordner",
    c1Desc: "Jedes Buch befindet sich in einem eigenen Ordner mit book.yaml, Kapiteldateien (.md), dem Ordner cover/ für Buchcover und images/ für Abbildungen.",
    c2Title: "2. Typografische Stile & Zierate",
    c2Desc: "Wählen Sie aus 4 Stilen: Classic (Literatur), Poetry (Lyrik und Theater), Modern (Sachbücher) und Adelphi (Europäische Buchkunst). Kapitelanfänge mit Initialen (Drop Caps) oder Kapitälchen.",
    c3Title: "3. Schreiben und Bilder einfügen",
    c3Desc: "Formatieren Sie Text mit der Toolbar oder Tastaturkürzeln. Fügen Sie Screenshots direkt aus der Zwischenablage (Cmd+V) ein – sie werden automatisch in images/ gespeichert.",
    c4Title: "4. Echte Publikationsvorschau",
    c4Desc: "Erstellt die echten EPUB- und PDF-Dateien. Die PDF-Vorschau zeigt exakte Buchseiten mit Beschnitt (5x8, 6x9 Zoll), Bundsteg und Doppelseiten. Ebook simuliert Reader, Tablet und Smartphone.",
    c5Title: "5. Arbeitsbereich & 1-Klick-Öffnen",
    c5Desc: "Legen Sie Ihren Arbeitsbereich fest. Ihre zuletzt geöffneten Bücher bleiben auf der Startseite sichtbar und öffnen sich mit einem Klick.",
    colAction: "Aktion",
    colShortcut: "Tastaturkürzel",
    colDesc: "Beschreibung",
    s1: "Fett",
    s1d: "**Fett** für markierten Text umschalten",
    s2: "Kursiv",
    s2d: "*Kursiv* für markierten Text umschalten",
    s3: "Link",
    s3d: "Markdown-Link [Text](url) einfügen",
    s4: "Speichern",
    s4d: "Sofort auf Festplatte speichern (Automatisches Speichern aktiv)",
    s5: "Bild einfügen",
    s5d: "Bild aus Zwischenablage direkt in images/ einfügen",
    s6: "Rückgängig",
    s6d: "Letzte Änderung rückgängig machen",
    s7: "Wiederholen",
    s7d: "Änderung wiederherstellen",
    s8: "Schließen",
    s8d: "Dialoge und Vorschau schließen"
  },
  pt: {
    heroTitle: "Bem-vindo ao PubCraft",
    heroDesc: "PubCraft é um estúdio artesanal de tipografia e publicação de livros. Converte seus manuscritos Markdown em EPUBs para Kindle e PDFs de impressão profissional com fidelidade tipográfica rigorosa.",
    c1Title: "1. Manuscrito e Pasta Vault",
    c1Desc: "Cada livro reside em uma pasta dedicada contendo book.yaml, arquivos Markdown (.md), a pasta cover/ para capas e images/ para ilustrações.",
    c2Title: "2. Estilos Tipográficos e Ornamentos",
    c2Desc: "4 estilos distintos: Classic (ficção literária), Poetry (poesia e teatro), Modern (não-ficção e ensaios) e Adelphi (tradição editorial). Aberturas com capitulares (Drop Caps) ou versaletes.",
    c3Title: "3. Escrita e Colagem de Imagens",
    c3Desc: "Formate com a barra minimalista ou atalhos. Cole capturas e imagens diretamente da área de transferência (Cmd+V) para salvá-las em images/ e inseri-las no texto.",
    c4Title: "4. Pré-visualização Real de Publicação",
    c4Desc: "Compila o fluxo real de EPUB e PDF. O visualizador PDF exibe páginas reais com formatos de corte (5x8, 6x9 pol.), margens e páginas espelhadas.",
    c5Title: "5. Área de Trabalho e Abertura em 1 Clique",
    c5Desc: "Defina sua pasta de trabalho padrão. Seus livros recentes permanecem visíveis na tela inicial para você retomar a escrita com 1 clique.",
    colAction: "Ação",
    colShortcut: "Atalho",
    colDesc: "Descrição",
    s1: "Negrito",
    s1d: "Alternar **negrito** no texto selecionado",
    s2: "Itálico",
    s2d: "Alternar *itálico* no texto selecionado",
    s3: "Link",
    s3d: "Inserir link [texto](url)",
    s4: "Salvar",
    s4d: "Salvar alterações imediatamente (o salvamento automático está ativo)",
    s5: "Colar Imagem",
    s5d: "Colar imagem da área de transferência diretamente em images/",
    s6: "Desfazer",
    s6d: "Reverter última alteração",
    s7: "Refazer",
    s7d: "Reaplicar alteração revertida",
    s8: "Fechar",
    s8d: "Fechar caixas de diálogo e pré-visualização"
  },
  zh: {
    heroTitle: "欢迎使用 PubCraft",
    heroDesc: "PubCraft 是一款专注排版美学的图书制作工作室。它能将您的 Markdown 原稿精确编译为规范的 Kindle EPUB 电子书与专业印刷级 PDF。",
    c1Title: "1. 书籍原稿与项目文件夹 (Vault)",
    c1Desc: "每本书位于独立的文件夹中，包含配置文件 book.yaml、章节 Markdown 文件 (.md)、用于封面的 cover/ 目录以及用于插图的 images/ 目录。",
    c2Title: "2. 排版风格与装饰",
    c2Desc: "内置 4 种排版风格：Classic（文学小说）、Poetry（诗歌戏剧）、Modern（当代随笔与非虚构）、Adelphi（欧式传统出版风貌）。支持首字下沉 (Drop Cap) 与小型大写字母。",
    c3Title: "3. 写作与剪贴板图片直接粘贴",
    c3Desc: "通过精简工具栏或快捷键快速设置标题、引用与列表。直接从剪贴板粘贴截图或图片 (Cmd+V)，系统会自动将其保存至 images/ 并在正文中生成标签。",
    c4Title: "4. 真实出版物预览",
    c4Desc: "预览功能直接编译真实 EPUB 与 PDF 字节流。PDF 预览忠实展示裁切尺寸（如 5x8、6x9 英寸）、装订订口与对开跨页效果；电子书模式支持多设备模拟。",
    c5Title: "5. 工作区与一键打开",
    c5Desc: "设定您的常用工作区文件夹。最近打开的书籍均陈列在欢迎启动界面中，点击即可瞬时进入图书编辑。",
    colAction: "操作",
    colShortcut: "快捷键",
    colDesc: "说明",
    s1: "粗体",
    s1d: "对选中文本切换 **粗体**",
    s2: "斜体",
    s2d: "对选中文本切换 *斜体*",
    s3: "链接",
    s3d: "插入 Markdown 链接 [文本](url)",
    s4: "立即保存",
    s4d: "立即将内容写入磁盘（自动保存始终生效）",
    s5: "粘贴图片",
    s5d: "从剪贴板直接粘贴图片至 images/ 目录",
    s6: "撤销",
    s6d: "撤销上一项编辑操作",
    s7: "重做",
    s7d: "恢复被撤销的操作",
    s8: "关闭窗口",
    s8d: "关闭模态窗口、文件选择器及预览"
  },
  ja: {
    heroTitle: "PubCraft へようこそ",
    heroDesc: "PubCraft は組版の美しさと品質を追求した書籍制作スタジオです。Markdown 原稿を完全準拠の Kindle EPUB や高品質な印刷用 PDF へ忠実にコンパイルします。",
    c1Title: "1. 原稿とプロジェクトフォルダ (Vault)",
    c1Desc: "1冊の書籍は book.yaml、各章の Markdown ファイル (.md)、表紙用の cover/ フォルダ、挿絵用の images/ フォルダから構成されます。",
    c2Title: "2. タイポグラフィスタイルと飾り",
    c2Desc: "Classic（文学・フィクション）、Poetry（詩歌・戯曲）、Modern（現代的ノンフィクション）、Adelphi（伝統的出版スタイル）の4スタイルを収録。ドロップキャップにも対応しています。",
    c3Title: "3. 執筆と画像の直接貼り付け",
    c3Desc: "見出しや引用、リストをすばやく書式設定可能。クリップボードからスクリーンショットや画像（Cmd+V）を直接貼り付けると、自動的に images/ に保存され挿入されます。",
    c4Title: "4. 本物の出版プレビュー",
    c4Desc: "実際の EPUB と PDF バイト列を生成して直接描画します。PDF プレビューでは判型（5×8、6×9 インチ等）、のどしろ余白、見開き表示を確認できます。",
    c5Title: "5. ワークスペースとワンクリック起動",
    c5Desc: "よく使う作業フォルダをワークスペースとして設定可能。最近開いた書籍はスタート画面に一覧表示され、1クリックですぐに執筆を再開できます。",
    colAction: "操作",
    colShortcut: "ショートカット",
    colDesc: "説明",
    s1: "太字",
    s1d: "選択テキストの **太字** を切り替え",
    s2: "斜体",
    s2d: "選択テキストの *斜体* を切り替え",
    s3: "リンク",
    s3d: "Markdown リンク [テキスト](url) を挿入",
    s4: "今すぐ保存",
    s4d: "ディスクへ即時保存（自動保存も常時有効）",
    s5: "画像を貼り付け",
    s5d: "クリップボードの画像を images/ に自動保存して挿入",
    s6: "元に戻す",
    s6d: "直前の変更を取り消す",
    s7: "やり直し",
    s7d: "取り消した変更をやり直す",
    s8: "閉じる",
    s8d: "モーダルやファイル選択、プレビューを閉じる"
  },
  ru: {
    heroTitle: "Добро пожаловать в PubCraft",
    heroDesc: "PubCraft — это студия верстки и подготовки книг к публикации. Программа компилирует рукописи Markdown в настоящие файлы Kindle EPUB и готовые к печати PDF с бескомпромиссным качеством.",
    c1Title: "1. Рукопись и папка проекта (Vault)",
    c1Desc: "Каждая книга хранится в отдельной папке, содержащей book.yaml, файлы глав Markdown (.md), папку cover/ для обложек и images/ для иллюстраций.",
    c2Title: "2. Типографические стили и буквицы",
    c2Desc: "4 стиля: Classic (художественная проза), Poetry (поэзия и драматургия), Modern (публицистика и эссе) и Adelphi (классические европейские издания). Поддерживаются буквицы (Drop Cap) и капитель.",
    c3Title: "3. Написание текста и вставка изображений",
    c3Desc: "Форматируйте заголовки, списки и цитаты. Вставляйте скриншоты и изображения прямо из буфера обмена (Cmd+V) — они автоматически сохраняются в папку images/ и связываются с текстом.",
    c4Title: "4. Реальный предпросмотр публикации",
    c4Desc: "Предпросмотр отображает реальные скомпилированные байты EPUB и PDF. В PDF видны формат страниц (5x8, 6x9 дюймов), поля, корешковый отступ и развороты.",
    c5Title: "5. Рабочая папка и открытие в 1 клик",
    c5Desc: "Назначьте постоянную рабочую папку. Ваши недавние проекты отображаются на начальном экране и открываются мгновенно в 1 клик.",
    colAction: "Действие",
    colShortcut: "Сочетание клавиш",
    colDesc: "Описание",
    s1: "Полужирный",
    s1d: "Переключить **полужирный** для выделенного текста",
    s2: "Курсив",
    s2d: "Переключить *курсив* для выделенного текста",
    s3: "Ссылка",
    s3d: "Вставить ссылку Markdown [текст](url)",
    s4: "Сохранить сейчас",
    s4d: "Принудительно сохранить на диск (автосохранение также активно)",
    s5: "Вставить изображение",
    s5d: "Вставить картинку из буфера прямо в images/",
    s6: "Отменить",
    s6d: "Отменить последнее действие",
    s7: "Повторить",
    s7d: "Повторить отмененное действие",
    s8: "Закрыть",
    s8d: "Закрыть окно, выбор файлов или предпросмотр"
  },
  ar: {
    heroTitle: "مرحبًا بك في PubCraft",
    heroDesc: "PubCraft هو استوديو متكامل لتنسيق الكتب ونشرها باحترافية تامة. يقوم بتجميع مخطوطات Markdown إلى ملفات Kindle EPUB حقيقية وملفات PDF جاهزة للطباعة بجودة عالية.",
    c1Title: "1. مجلد المشروع والمخطوطة",
    c1Desc: "يحتوي كل كتاب على مجلد مخصص يضم ملف book.yaml، وفصول Markdown (.md)، ومجلد cover/ لأغلفة الكتب، ومجلد images/ للرسوم التوضيحية.",
    c2Title: "2. الأنماط الطباعية والزخارف",
    c2Desc: "يتوفر 4 أنماط متميزة: Classic (للروايات الأدبية)، Poetry (للشعر والمسرح)، Modern (للمقالات والكتب المعاصرة)، و Adelphi (للطباعة الكلاسيكية الأوروبية). مع دعم الحروف الاستهلالية الكبيرة.",
    c3Title: "3. الكتابة ولصق الصور مباشرة",
    c3Desc: "نسّق العناوين والاقتباسات والقوائم بسهولة. يمكنك لصق لقطات الشاشة والصور مباشرة من الحافظة (Cmd+V) لحفظها تلقائيًا داخل مجلد images/ وربطها بالمستند.",
    c4Title: "4. معاينة النشر الحقيقية",
    c4Desc: "تُنشئ المعاينة ملفات EPUB و PDF الحقيقية. يعرض عارض PDF الصفحات بأحجام الطباعة الدقيقة (مثل 5x8 و 6x9 بوصة) وهوامش التجليد والصفحات المتقابلة.",
    c5Title: "5. مساحة العمل والفتح بنقرة واحدة",
    c5Desc: "حدّد مجلد العمل المفضل لديك. ستبقى كتبك ومشاريعك الأخيرة ظاهرة في شاشة البدء لتفتح كتابك وتباشر الكتابة بنقرة واحدة فقط.",
    colAction: "الإجراء",
    colShortcut: "الاختصار",
    colDesc: "الوصف",
    s1: "عريض",
    s1d: "تبديل النص المحدد إلى **عريض**",
    s2: "مائل",
    s2d: "تبديل النص المحدد إلى *مائل*",
    s3: "رابط",
    s3d: "إدراج رابط markdown [نص](url)",
    s4: "حفظ الآن",
    s4d: "حفظ التغييرات فورًا على القرص (الحفظ التلقائي مفعّل دائمًا)",
    s5: "لصق صورة",
    s5d: "لصق صورة من الحافظة وحفظها تلقائيًا في images/",
    s6: "تراجع",
    s6d: "التراجع عن التعديل الأخير",
    s7: "إعادة",
    s7d: "إعادة تطبيق التعديل المتراجع عنه",
    s8: "إغلاق",
    s8d: "إغلاق النوافذ المنبثقة والمعاينة"
  }
};

function getTutorialHTML(lang, tab) {
  const d = TUTORIAL_DATA[lang] || TUTORIAL_DATA["en"];
  if (tab === "shortcuts") {
    const isMac = navigator.platform.toUpperCase().indexOf("MAC") >= 0;
    const mod = isMac ? "Cmd" : "Ctrl";
    const altMod = isMac ? "Option" : "Alt";
    return `
      <table class="shortcut-table">
        <thead>
          <tr>
            <th>${d.colAction}</th>
            <th>${d.colShortcut}</th>
            <th>${d.colDesc}</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td><b>${d.s1}</b></td>
            <td><kbd>${mod}+B</kbd></td>
            <td>${d.s1d}</td>
          </tr>
          <tr>
            <td><b>${d.s2}</b></td>
            <td><kbd>${mod}+I</kbd></td>
            <td>${d.s2d}</td>
          </tr>
          <tr>
            <td><b>${d.s3}</b></td>
            <td><kbd>${mod}+K</kbd></td>
            <td>${d.s3d}</td>
          </tr>
          <tr>
            <td><b>${d.s4}</b></td>
            <td><kbd>${mod}+S</kbd></td>
            <td>${d.s4d}</td>
          </tr>
          <tr>
            <td><b>${d.s5}</b></td>
            <td><kbd>${mod}+V</kbd></td>
            <td>${d.s5d}</td>
          </tr>
          <tr>
            <td><b>${d.s6}</b></td>
            <td><kbd>${mod}+Z</kbd></td>
            <td>${d.s6d}</td>
          </tr>
          <tr>
            <td><b>${d.s7}</b></td>
            <td><kbd>${isMac ? "Cmd+Shift+Z" : "Ctrl+Y"}</kbd></td>
            <td>${d.s7d}</td>
          </tr>
          <tr>
            <td><b>${d.s8}</b></td>
            <td><kbd>Esc</kbd></td>
            <td>${d.s8d}</td>
          </tr>
          <tr>
            <td><b>${d.s9 || "Focus Mode"}</b></td>
            <td><kbd>F11</kbd> / <kbd>${mod}+Shift+F</kbd></td>
            <td>${d.s9d || "Toggle distraction-free focus mode"}</td>
          </tr>
          <tr>
            <td><b>${d.s10 || "Sidebar"}</b></td>
            <td><kbd>${mod}+\\</kbd></td>
            <td>${d.s10d || "Toggle left sidebar visibility"}</td>
          </tr>
          <tr>
            <td><b>${d.s11 || "Command Palette"}</b></td>
            <td><kbd>${mod}+K</kbd></td>
            <td>${d.s11d || "Cerca capitoli, note o comandi rapidi"}</td>
          </tr>
        </tbody>
      </table>
    `;
  }

  // Guide tab
  return `
    <div class="tut-hero">
      <h4>${d.heroTitle}</h4>
      <p style="margin:0;font-size:13px;color:var(--ink);">${d.heroDesc}</p>
    </div>
    <div class="tut-grid">
      <div class="tut-card">
        <h5>${d.c1Title}</h5>
        <p>${d.c1Desc}</p>
      </div>
      <div class="tut-card">
        <h5>${d.c2Title}</h5>
        <p>${d.c2Desc}</p>
      </div>
      <div class="tut-card">
        <h5>${d.c3Title}</h5>
        <p>${d.c3Desc}</p>
      </div>
      <div class="tut-card">
        <h5>${d.c4Title}</h5>
        <p>${d.c4Desc}</p>
      </div>
    </div>
    <div class="tut-card" style="margin-top:4px;">
      <h5>${d.c5Title}</h5>
      <p>${d.c5Desc}</p>
    </div>
  `;
}
