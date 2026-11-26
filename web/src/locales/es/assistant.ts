// Spanish UI strings (see src/lib/i18n.ts). Keys are the English text exactly as passed to t()/tn().
export default {
  // Assistant
  'No changes': 'Sin cambios',
  'Changes applied': 'Cambios aplicados',
  'Changes rejected': 'Cambios rechazados',
  Stopped: 'Detenido',
  'Fix the compile errors': 'Corrige los errores de compilación',
  'Check the grammar of the open file': 'Revisa la gramática del archivo abierto',
  'Make the abstract more concise': 'Haz el resumen más conciso',
  'The assistant is busy with another request.': 'El asistente está ocupado con otra petición.',
  'New chat': 'Chat nuevo',
  'Open assistant': 'Abrir asistente',
  'Assistant is working…': 'El asistente está trabajando…',
  'Changes ready to review': 'Cambios listos para revisar',
  Assistant: 'Asistente',
  'Resize assistant': 'Cambiar tamaño del asistente',
  'Collapse assistant': 'Contraer asistente',
  Collapse: 'Contraer',
  'Recent chats': 'Chats recientes',
  'Collapse to input bar': 'Contraer a barra de entrada',
  'Minimize to bubble': 'Minimizar a burbuja',
  'Ask for a change': 'Pide un cambio',
  'The assistant reads your project, edits a copy and proposes a diff. Nothing changes until you accept.':
    'El asistente lee tu proyecto, edita una copia y propone un diff. Nada cambia hasta que aceptes.',
  Thinking: 'Pensando',
  '{n} file changed': '{n} archivo modificado',
  '{n} files changed': '{n} archivos modificados',
  Review: 'Revisar',
  'Open chat': 'Abrir chat',
  'Open settings': 'Abrir ajustes',
  'Ask for a change, or ask about the document…': 'Pide un cambio o pregunta sobre el documento…',
  'Ask anything': 'Pregunta lo que quieras',
  'Message the assistant': 'Mensaje para el asistente',
  'Sent with your message': 'Se envía con tu mensaje',
  'Show chat': 'Mostrar chat',
  Stop: 'Detener',
  Send: 'Enviar',
  'Worked · {n} step': 'Trabajado · {n} paso',
  'Worked · {n} steps': 'Trabajado · {n} pasos',
  '… and {n} more': '… y {n} más',
  'Reviewing…': 'Revisando…',
  'Review changes': 'Revisar cambios',
  'Manage usage': 'Gestionar uso',
  'Copy reply': 'Copiar respuesta',
  Copied: 'Copiado',
  Copy: 'Copiar',

  // ModelPicker
  'Search models': 'Buscar modelos',
  'No models match': 'Ningún modelo coincide',
  None: 'Ninguno',
  Minimal: 'Mínimo',
  Low: 'Bajo',
  Medium: 'Medio',
  High: 'Alto',
  'Extra high': 'Muy alto',
  Max: 'Máximo',
  'Model and reasoning': 'Modelo y razonamiento',
  Reasoning: 'Razonamiento',
  default: 'por defecto',
  Model: 'Modelo',

  // PlanBadge, Welcome
  'Using ChatGPT plan': 'Usando el plan de ChatGPT',
  'You’re using your ChatGPT plan': 'Estás usando tu plan de ChatGPT',
  'Eligible AI requests in Refract use your ChatGPT plan. You can review and manage this usage in':
    'Las peticiones de IA aptas en Refract usan tu plan de ChatGPT. Puedes revisar y gestionar este uso en los',
  'ChatGPT settings': 'ajustes de ChatGPT',
  'Got it': 'Entendido',

  // History
  'Already identical: nothing to restore.': 'Ya es idéntico: no hay nada que restaurar.',
  'Restored {path}': '{path} restaurado',
  'Restored the project': 'Proyecto restaurado',
  'Project history': 'Historial del proyecto',
  'Every saved version of the project, what changed, and restoring older versions.':
    'Cada versión guardada del proyecto, qué cambió y cómo restaurar versiones anteriores.',
  History: 'Historial',
  'Loading…': 'Cargando…',
  'Replace every file with this version? The current one stays in history.':
    '¿Reemplazar todos los archivos por esta versión? La actual se queda en el historial.',
  Cancel: 'Cancelar',
  Restore: 'Restaurar',
  'Restore project to this version': 'Restaurar el proyecto a esta versión',
  'Loading changes…': 'Cargando cambios…',
  'No file changes in this version.': 'No hay cambios de archivos en esta versión.',
  added: 'añadido',
  modified: 'modificado',
  deleted: 'eliminado',
  'Restore deleted file': 'Restaurar archivo eliminado',
  'Restore this version': 'Restaurar esta versión',
  'Binary file.': 'Archivo binario.',

  // CiteArxiv
  Search: 'Buscar',
  Cite: 'Citar',

  // api.ts
  'No AI connected. Sign in with ChatGPT (run `refract login` and upload the credentials) or add an API provider in Settings → Assistant.':
    'No hay IA conectada. Inicia sesión con ChatGPT (ejecuta `refract login` y sube las credenciales) o añade un proveedor por API en Ajustes → Asistente.',
  'ChatGPT plan usage was not granted. Run `refract login` again and allow plan usage.':
    'No se concedió el uso del plan de ChatGPT. Vuelve a ejecutar `refract login` y permite el uso del plan.',
  'You have reached a usage limit on your ChatGPT plan or for this app. Review it in ChatGPT settings → Usage.':
    'Has alcanzado un límite de uso de tu plan de ChatGPT o de esta app. Revísalo en los ajustes de ChatGPT → Uso.',
  'ChatGPT plan usage is not available for this account: {message}':
    'El uso del plan de ChatGPT no está disponible para esta cuenta: {message}',
  'This model is not available. Pick another one.': 'Este modelo no está disponible. Elige otro.',
  'The provider rejected the API key: {message}': 'El proveedor ha rechazado la clave de API: {message}',
  'The provider has no credit left on this API key: {message}':
    'Esta clave de API no tiene crédito disponible en el proveedor: {message}',
  'Request not supported on plan usage ({param}): {message}':
    'Petición no admitida con el uso del plan ({param}): {message}',
  unknown: 'desconocido',
  'The AI provider is temporarily unavailable. Try again shortly. ({message})':
    'El proveedor de IA no está disponible temporalmente. Vuelve a intentarlo en breve. ({message})',
  'Download failed: HTTP {status}': 'Error en la descarga: HTTP {status}',
  'Add a reference': 'Añadir una referencia',
  'Find a paper that supports the selected text, or paste a DOI, arXiv id or URL. The citation is placed in your selection for you to review.':
    'Busca un artículo que respalde el texto seleccionado, o pega un DOI, un id de arXiv o una URL. La cita se coloca en tu selección para que la revises.',
  'Search published papers and arXiv, or paste a DOI, arXiv id or URL. \\cite is inserted at the cursor.':
    'Busca artículos publicados y en arXiv, o pega un DOI, un id de arXiv o una URL. Se inserta \\cite en el cursor.',
  'Find supporting papers': 'Buscar artículos que lo respalden',
  'Keywords, a title, a DOI, an arXiv id or a URL': 'Palabras clave, un título, un DOI, un id de arXiv o una URL',
  'Search papers': 'Buscar artículos',
  'Looking for papers that support it…': 'Buscando artículos que lo respalden…',
  'No suitable papers found. Try a search with your own keywords.':
    'No se encontraron artículos adecuados. Prueba una búsqueda con tus propias palabras clave.',
  'Search by keywords, title, author or id.': 'Busca por palabras clave, título, autor o id.',
  'cited {n} time': 'citado {n} vez',
  'cited {n} times': 'citado {n} veces',
  'Float over the editor': 'Flotar sobre el editor',
  'Dock to the side': 'Anclar a un lado',
} as Record<string, string>
