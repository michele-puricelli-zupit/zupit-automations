# Esempi email e modelli

I file in `testdata/` derivano dagli esempi incollati nella conversazione e dai quattro file `.eml` forniti successivamente. I nuovi campioni sono versioni ridotte: conservano i campi utili e le strutture MIME/HTML rilevanti, sostituendo destinatari personali, dettagli identificativi e link con valori di test. Le intestazioni di trasporto e i link di tracking non sono copiati. Nessun link viene aperto dai test.

| Campione | Uso |
|---|---|
| `srt-assignment.eml` | Notifica di assegnazione `TAS000000979470` al gruppo `AS - SRT`, origine `INC000002356016`. |
| `key2help-assignment.eml` | Assegnazione `SR-123827`, progetto `PGD - Configurazioni Digit_R35_R7`, servizio `E-Service [PGD - SAP CRM]`. |
| `cup-jira-result.eml` | Risultato dell'automazione esistente: ticket `APPSD-238` già creato. È un caso da escludere dalla creazione CUP, non un esempio dell'email sorgente del modulo. |
| `srt-forwarded.eml` | Inoltro della notifica `TAS000000978007`, origine `INC000002352488`, problema sulla tabella `PRODUZIONE_STANDARD`. |
| `key2help-html.eml` | Struttura HTML dell'assegnazione `SR-123827`, con oggetto originale e mittente `servicedesk4@siag.it`. |
| `cup-forwarded.eml` | Inoltro del modulo Google Sheet per un bug POST Agende CUP; il mittente originale è `david.tacconi@asuit.tn.it`. |
| `support-jira-digest.eml` | Riepilogo Jira ricevuto tramite `support_kr@zupit.it`, da escludere anche se il mittente normalizzato non contiene `jira`. |

I campioni precedenti `srt-assignment`, `key2help-assignment` e `cup-jira-result` mantengono intestazioni sintetiche. Nei due nuovi inoltri `From` identifica chi inoltra, non il mittente originale nel corpo. Per simulare l'arrivo diretto di SRT o CUP, impostare `From` rispettivamente a `support@tndigit.it` o `david.tacconi@asuit.tn.it` e togliere `Fwd: ` dall'oggetto. I test verificano sia il rifiuto dell'inoltro da un altro mittente sia il caso diretto ricostruito; il parser non promuove le intestazioni citate nel corpo a intestazioni attendibili.

## SRT

Il modello richiede il mittente `support@tndigit.it`, ricavato dall'intestazione dell'inoltro, e una riga `Gruppo: AS - SRT`. Estrae `task_id`, `incident_id`, `summary`, `priority` e `support_group`. Task e gruppo sono obbligatori; gli altri valori possono essere assenti. Il ticket conserva oggetto e corpo originali. La priorità testuale `Low` è disponibile per mapping successivi; non viene convertita in un ID Jira inventato. L'esclusione SBC resta prima del ramo di creazione.

## key2help

Il modello richiede `servicedesk4@siag.it` e riconosce la riga `Ticket in ky2help: ...` (anche con grafia `key2help`), il testo `Il Task (...)` e il progetto tra virgolette dritte o tipografiche. Estrae `reference`, `summary`, `project`, `service`, `support_group` e `ticket_url`. Le condizioni dei rami Workflow e Orbeon si riferiscono al progetto, non al prefisso `SR`; i valori da riconoscere e gli ID delle liste restano da configurare. Il ramo Foundation resta il caso finale del modello e va verificato con il mapping Make.

La colonna titolo contiene riferimento e riepilogo del task. Le altre variabili si possono aggiungere nei mapping delle colonne reali. `ticket_url` è facoltativo. Nel file HTML fornito `Accedi al ticket` usa spazi non separabili ed è seguito dal pulsante con etichetta `SR-123827`: il modello riconosce questa struttura e recupera il suo URL. Continua ad accettare il link direttamente su `Accedi al ticket`, senza confondere il collegamento documentale SharePoint con quello del ticket. Nel precedente campione incollato, privo del link, `ticket_url` resta vuoto.

## CUP

Dal risultato è confermato il progetto `APPSD` per il ramo bug. Il nome visualizzato del richiedente non fornisce il suo account ID; `Bug` e `Medium` non forniscono gli ID API di tipo e priorità. Questi valori non sono dedotti automaticamente.

Il mittente del modulo è `david.tacconi@asuit.tn.it`, confermato dall'utente e dall'intestazione citata nell'inoltro. Il filtro sull'oggetto è `^Nuova segnalazione problema software POST$`; questo caso sceglie il ramo `Segnalazione bug` e conserva tutto il corpo, compresi segnalante, tipologia, urgenza e descrizione. Il filtro continua a escludere la notifica italiana con riga `... ha creato un ticket`.

Il campione copre solo il bug: per `Unità erogante` e `Suggerimento al PO` servono gli oggetti e i criteri originali, da aggiungere anche al filtro generale. Board, tipi, account ID del richiedente e destinatario PO restano da configurare. L'email segnalante non viene convertita automaticamente in un account ID Jira. Un ulteriore test sintetico verifica la conservazione di virgolette e link.

## Support KR

Il campione è un caso negativo: un riepilogo settimanale Jira, non una nuova richiesta di supporto. L'intestazione `From` contiene il nome Jira ma l'indirizzo normalizzato è `support_kr@zupit.it`; `Reply-To` è `jira@zupit-pro.atlassian.net`. Il modello esclude `jira` sia in `from` sia in `reply_to`. Il campo `reply_to` è disponibile nei filtri e nei template ed è letto sia dal parser RFC822 sia dalle API Gmail. Questo controllo è un filtro operativo, non una verifica di autenticità del mittente. Manca ancora una segnalazione Support KR positiva per verificare gli altri criteri del flusso.

Tutti i modelli restano disattivati. I test completano connessioni e destinazioni con valori sintetici e non effettuano scritture esterne. Per utilizzare le nuove regole in un flusso già salvato, aggiornarlo esplicitamente nell'editor o ricrearlo dal modello.
