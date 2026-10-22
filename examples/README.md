# Esempi email e modelli

I file in `testdata/` derivano dai tre esempi incollati nella conversazione. Gli indirizzi delle intestazioni RFC822 sono sintetici; per key2help anche l'oggetto è sintetico, perché non era presente nel testo. Indirizzi personali e collegamenti documentali sono sostituiti con valori di test. Nessun link viene aperto dai test.

| Campione | Uso |
|---|---|
| `srt-assignment.eml` | Notifica di assegnazione `TAS000000979470` al gruppo `AS - SRT`, origine `INC000002356016`. |
| `key2help-assignment.eml` | Assegnazione `SR-123827`, progetto `PGD - Configurazioni Digit_R35_R7`, servizio `E-Service [PGD - SAP CRM]`. |
| `cup-jira-result.eml` | Risultato dell'automazione esistente: ticket `APPSD-238` già creato. È un caso da escludere dalla creazione CUP, non un esempio dell'email sorgente del modulo. |

## SRT

Il modello richiede il mittente da configurare e una riga `Gruppo: AS - SRT`. Estrae `task_id`, `incident_id`, `summary`, `priority` e `support_group`. Task e gruppo sono obbligatori; gli altri valori possono essere assenti. Il ticket conserva oggetto e corpo originali. La priorità testuale `Low` è disponibile per mapping successivi; non viene convertita in un ID Jira inventato. L'esclusione SBC resta prima del ramo di creazione.

## key2help

Il modello riconosce la riga `Ticket in ky2help: ...` (anche con grafia `key2help`), il testo `Il Task (...)` e il progetto tra virgolette dritte o tipografiche. Estrae `reference`, `summary`, `project`, `service`, `support_group` e `ticket_url`. Le condizioni dei rami Workflow e Orbeon si riferiscono al progetto, non al prefisso `SR`; i valori da riconoscere e gli ID delle liste restano da configurare. Il ramo Foundation resta il caso finale del modello e va verificato con il mapping Make.

La colonna titolo contiene riferimento e riepilogo del task. Le altre variabili si possono aggiungere nei mapping delle colonne reali. `ticket_url` è facoltativo e viene valorizzato solo quando il testo del pulsante `Accedi al ticket` è seguito dal suo URL, come prodotto dal parser HTML per un normale link. Nel campione incollato l'URL del pulsante manca: il link documentale SharePoint non viene usato come URL del ticket. Per altre strutture HTML servono ulteriori campioni originali.

## CUP

Dal risultato è confermato il progetto `APPSD` per il ramo bug. Il nome visualizzato del richiedente non fornisce il suo account ID; `Bug` e `Medium` non forniscono gli ID API di tipo e priorità. Questi valori non sono dedotti automaticamente.

Il filtro esclude la notifica italiana con riga `... ha creato un ticket`, riconosciuta nell'esempio. Mittente e oggetto dell'email originale del modulo, criteri degli altri rami, board, tipi e destinatario PO restano da configurare. Il test positivo CUP usa un input sintetico e verifica la conservazione di descrizione, virgolette e link; il messaggio fornito viene testato come notifica da escludere.

Tutti i modelli restano disattivati. I test completano connessioni e destinazioni con valori sintetici e non effettuano scritture esterne. Per utilizzare le nuove regole in un flusso già salvato, aggiornarlo esplicitamente nell'editor o ricrearlo dal modello.
