# Applicativo Go per sostituire Make

## Obiettivo e perimetro

Realizzare un’applicazione aziendale per elaborare le email Gmail all’arrivo e creare ticket Jira, elementi nelle Slack List oppure inoltri email al PO.

La prima versione comprende **interfaccia basilare, connessioni configurabili e creazione libera di flussi** entro queste integrazioni. Parsing e classificazione saranno deterministici. JEV/AI, editor grafico e connettori ulteriori restano fuori dalla prima versione.

## Architettura e configurazione

- **Servizio Go unico**, con Chi, Templ, HTMX e PostgreSQL/Ent, seguendo l’impostazione attuale di `zupit-eventi`. Deploy Docker/Portainer con database, segreti e configurazioni dedicati.
- **Accesso Google Workspace:** adattare da `zupit-eventi` verifica dell’identità e dei domini autorizzati, sessioni sul server, cookie protetti e CSRF. Amministratori configurati al primo deploy; altri utenti aziendali in sola lettura.
- **Gmail:** collegamento OAuth della casella di team, separato dal login dell’utente. Permessi di lettura e invio necessari per elaborazione e inoltro al PO.
- **Jira Cloud:** istanza iniziale `zupit-pro.atlassian.net`, configurabile. Connessione tramite account tecnico e API token, con verifica dei permessi su progetti e richiedenti.
- **Slack:** connessione tramite Slack App aziendale; configurazione delle liste, delle colonne e del canale operativo per gli errori. Scrittura diretta tramite [API Slack Lists](https://docs.slack.dev/reference/methods/slackLists.items.create/).
- Token e credenziali cifrati nel database con chiave fornita al deploy; mai restituiti nelle pagine o nei log.

L’interfaccia avrà tre aree: **Connessioni**, **Flussi**, **Esecuzioni**. Gli amministratori potranno verificare e ricollegare connessioni, creare/modificare/attivare flussi, simulare l’elaborazione e riprendere gli errori.

## Flussi e destinazioni

Un flusso sarà definito da: connessione Gmail, condizioni, estrazioni, rami ordinati e azioni.

- Condizioni su mittente, destinatari, oggetto e corpo: uguaglianza, prefisso, contenimento ed espressioni regolari, combinabili con AND/OR.
- Estrazioni tramite campi email ed espressioni regolari con gruppi nominati; composizione dei valori tramite costanti e template.
- Primo ramo corrispondente all’interno del singolo flusso; **tutti i flussi attivi vengono valutati indipendentemente**.
- Azioni disponibili: creazione ticket Jira, creazione elemento Slack List, inoltro email oppure esclusione motivata.
- Configurazioni validate prima dell’attivazione. Le esecuzioni conservano la configurazione usata, evitando che modifiche successive cambino un’elaborazione già avviata.

| Flusso iniziale | Comportamento |
|---|---|
| SRT/Remedy | Riconoscere le notifiche e creare il ticket Jira con oggetto email come riepilogo e richiedente configurato. Escludere esplicitamente SBC. |
| CUP | Verificare mittente e oggetto. Creare ticket per bug e unità erogante, con i rispettivi tipi; inoltrare i suggerimenti al PO configurato. |
| Key/Kit to Help | Riconoscere call e SR, estrarre riferimento e collegamento, classificare il progetto e creare l’elemento nella Slack List corretta. |
| Support KR | Applicare i filtri attuali, escludere le notifiche Jira e creare l’elemento nella lista configurata. |

Per ogni azione Jira si configureranno **progetto, board attesa, tipo di ticket, richiedente e campi necessari**. La presenza nella board dipende dal suo filtro: l’app valorizzerà i campi previsti e controllerà la destinazione risultante. [Filtri delle board Jira](https://support.atlassian.com/jira-software-cloud/docs/configure-filters/)

Le automazioni Jira esistenti continueranno a gestire **sprint attivo e notifiche Slack**. Se il ticket viene creato ma non risulta nella board attesa, l’app segnalerà l’anomalia senza creare un secondo ticket.

## Ricezione, affidabilità e test

- Ricevere gli eventi Gmail tramite Pub/Sub **StreamingPull**, con connessione persistente gestita dal client ufficiale. L’elaborazione parte sugli eventi, senza attendere un intervallo di polling. [Documentazione Pub/Sub](https://docs.cloud.google.com/pubsub/docs/pull)
- Rinnovare giornalmente l’iscrizione Gmail e verificare ogni cinque minuti eventuali cambiamenti mancanti tramite timer interno. Recuperare anche al riavvio; gestire la cronologia Gmail scaduta con risincronizzazione. [Documentazione Gmail](https://developers.google.com/workspace/gmail/api/guides/push)
- Salvare email necessarie all’elaborazione, avanzamento della sincronizzazione e lavori nel database prima di confermare la ricezione. Usare una coda persistente nello stesso PostgreSQL.
- Identificare ogni azione tramite casella, messaggio Gmail, flusso e azione. Conservare gli esiti già riusciti durante retry e riavvii.
- Per errori temporanei: tentativo iniziale e due retry con attesa crescente, rispettando gli eventuali tempi indicati dal servizio. Credenziali revocate richiedono riconnessione; le altre connessioni continuano a funzionare.
- Timeout con esito esterno incerto: stato “da verificare”, senza ripetizione automatica della creazione. Avvisi sul canale Slack e stato sempre disponibile nell’interfaccia.

I test copriranno:

- Tutti i rami dei quattro flussi, esclusioni SBC/Jira e attivazione di più flussi sulla stessa email.
- Email MIME/HTML, virgolette, accenti, campi mancanti e formati inattesi; serializzazione corretta senza sostituzioni distruttive del testo.
- Mapping Jira/Slack, richiedente, board corretta e assegnazione allo sprint da parte delle regole Jira.
- Duplicati, riavvii, recupero eventi, errori di autenticazione, rate limit ed esiti incerti.
- Login aziendale, permessi amministratore, CSRF e protezione dei segreti.
- Simulazione senza scritture esterne e misura della latenza reale dall’email alla destinazione.

## Realizzazione e passaggio da Make

1. Raccogliere esportazioni degli scenari Make ed email rappresentative di ogni ramo; ricavare filtri e mapping esatti.
2. Preparare repository, comandi di verifica e deploy seguendo le convenzioni effettive di `zupit-eventi`.
3. Implementare accesso, connessioni e modello persistente di flussi/esecuzioni.
4. Implementare ricezione Gmail, recupero, editor dei flussi e connettori.
5. Configurare i quattro flussi iniziali e confrontare le simulazioni con gli esiti attesi degli scenari Make.
6. Validare scritture e automazioni Jira in destinazioni di prova, quindi verificare le destinazioni reali.
7. Migrare uno scenario alla volta: disattivare il corrispondente Make, registrare il punto di passaggio e attivare il nuovo flusso. Nessuna elaborazione automatica dello storico precedente.
8. Per il rollback, fermare prima il nuovo flusso e riconciliare le email dell’intervallo prima di riattivare Make.

**Prerequisiti all’attivazione:** casella di team destinataria delle notifiche, campioni email, scenari Make, mapping progetto/board/campi Jira, liste/colonne Slack e indirizzo del PO. I prefissi trascritti in modo incerto non saranno inventati.

Non sono ancora stati verificati accessi reali, configurazioni Make e permessi delle destinazioni. La latenza dipende dai servizi esterni; un’email eliminata prima del recupero potrebbe non essere più elaborabile.
