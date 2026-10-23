# Zupit Automazioni

Servizio Go per sostituire gli scenari Make: Gmail → condizioni/estrazioni → Jira Cloud, Slack List o inoltro email. Interfaccia Chi/templ/HTMX, login Google Workspace e coda persistente PostgreSQL/Ent. Il piano concordato è conservato in [PLAN.md](PLAN.md).

## Stato

Sono implementati login e ruoli, connessioni cifrate, editor, simulazione RFC822, ricezione Pub/Sub StreamingPull, recupero Gmail, coda, retry, riconciliazione manuale e avvisi Slack. I quattro modelli SRT, CUP, key2help e Support KR si aprono da **Flussi** come bozze; scegliere una sorgente Gmail per salvarli. Tutti i valori `DA_CONFIGURARE` devono essere sostituiti prima dell'attivazione.

Sono disponibili esempi SRT e key2help, un bug dal modulo CUP e un riepilogo Jira da escludere per Support KR, oltre al risultato CUP già creato da Make/Jira. I [campioni di test](examples/README.md) sono ripuliti e ridotti dai messaggi forniti. I modelli includono i mittenti osservati; gli inoltri SRT e CUP restano distinti dagli arrivi diretti. Mancano i casi degli altri rami, una richiesta Support KR positiva, le esportazioni Make, le credenziali e i mapping definitivi. I modelli non sono scenari pronti per la produzione. Scritture reali, board/sprint Jira e latenza dall'arrivo dell'email richiedono collaudo sulle destinazioni aziendali.

## Configurazione Google e destinazioni

1. Nel progetto Google Cloud abilitare Gmail API e Pub/Sub API. Creare un client OAuth **Web** con consenso interno all'organizzazione e queste callback esatte:
   - `APP_BASE_URL/auth/callback` per il login;
   - `APP_BASE_URL/auth/gmail/callback` per collegare una casella.
2. Autorizzare gli scope `openid`, `email`, `profile`, `gmail.readonly` e `gmail.send`. L'app richiede gli scope Gmail solo durante il collegamento della casella; usare una casella Workspace reale, anche diversa dall'account amministratore.
3. Creare un topic nel medesimo progetto del client OAuth e una subscription **pull**. Dare a `gmail-api-push@system.gserviceaccount.com` il ruolo Publisher sul topic. Fornire al server credenziali ADC con ruolo Subscriber sulla subscription. Non è necessario un webhook Pub/Sub pubblico. [Configurazione Gmail push](https://developers.google.com/workspace/gmail/api/guides/push).
4. Per Jira usare un account tecnico con API token e permessi di creazione ticket, accesso ai campi e modifica del richiedente quando richiesta. Nell'editor scegliere **Jira Board · crea ticket** oppure **Jira Service Management · crea richiesta**. Dopo aver scelto la connessione, i menu mostrano progetti e Service Desk per **nome e key**, board, tipi di ticket/richiesta e code per **nome**: non occorre inserire ID. Le board non hanno una key: si sceglie prima il progetto tramite la sua key e poi la board per nome. Le scelte si aggiornano automaticamente; **Aggiorna scelte Jira** permette di riprovare o proseguire senza HTMX. Cambiare connessione, progetto o Service Desk azzera le selezioni dipendenti. Gli ID restano interni e i flussi esistenti rimangono compatibili; se il caricamento fallisce, i valori salvati sono conservati. L'account ID del richiedente è facoltativo, nella sezione avanzata. Per Service Management si può scegliere una **coda attesa**: l'account deve essere agente del Service Desk per elencarla e verificarla. L'assenza di questi permessi non impedisce di scegliere il tipo di richiesta. Il controllo delle destinazioni all'attivazione è in sola lettura; verificare anche i permessi di scrittura con un ticket di prova.
5. Installare una Slack App con bot token e scope `lists:read`, `lists:write`, `chat:write`. Condividere le liste con l'app e aggiungere il bot al canale avvisi. Servono ID reali di liste e colonne. Le Slack List richiedono un workspace con piano a pagamento. [Lettura liste](https://docs.slack.dev/reference/methods/slackLists.items.list/), [creazione elementi](https://docs.slack.dev/reference/methods/slackLists.items.create/).

## Avvio Docker

Richiede Docker Compose v2, accesso alle API Google e i segreti sopra indicati.

```sh
cp .env.example .env
mkdir -p secrets
chmod 700 secrets
openssl rand -hex 32 > secrets/postgres_password
openssl rand -hex 32 > secrets/csrf_secret
openssl rand -base64 32 > secrets/encryption_key
```

Completare `.env` con client ID, domini e email degli amministratori. Scrivere in `secrets/database_url` una singola riga, sostituendo `PASSWORD` con il contenuto di `secrets/postgres_password`:

```text
postgres://automations:PASSWORD@postgres:5432/automations?sslmode=disable
```

Salvare il client secret in `secrets/google_client_secret` e le credenziali ADC in `secrets/google_credentials.json`. Non rigenerare la chiave di cifratura dopo il collegamento dei servizi: serve per decifrare i token esistenti. Conservare la chiave insieme alla procedura di ripristino del database, fuori dal repository.

Il processo applicativo usa UID/GID `10001:10001`: i file `database_url`, `google_client_secret`, `csrf_secret`, `encryption_key` e `google_credentials.json` devono essere leggibili da questo UID nel container. Su Linux, assegnare i file a UID 10001 e limitarli a modalità `0400`; la directory host può restare privata all'operatore. `postgres_password` deve essere leggibile dall'entrypoint PostgreSQL. Compose monta i segreti da file, mantenendo i vincoli del filesystem host. [Segreti Compose](https://docs.docker.com/compose/how-tos/use-secrets/).

```sh
docker compose config --quiet
docker compose up --build -d
docker compose ps -a
docker compose logs --tail=100 migrate app
```

Le migrazioni vengono applicate prima dell'applicazione. Aprire `http://localhost:8080`, accedere come amministratore, creare le connessioni e verificarle. Per Gmail salvare la connessione, quindi usare **Autorizza Google** selezionando la casella configurata. Nessuno scenario viene attivato automaticamente.

Se il progetto è stato spostato, il comando **Start** di Docker Desktop riusa i percorsi dei segreti registrati nei vecchi container. Dalla nuova cartella eseguire `docker compose up --build -d --force-recreate` per aggiornare i mount conservando il volume PostgreSQL. Non usare `down -v`: eliminerebbe il database.

`/health/live` verifica il processo; `/health/ready` verifica il database. Il server rifiuta l'avvio se le migrazioni sono mancanti o modificate dopo l'applicazione.

## Confronto A/B con Jev · opzionale

Ogni flusso può usare **solo A** (impostazione iniziale) oppure **A+B** sulle stesse nuove email. A esegue le regole standard. B usa Jev per proporre uno dei rami già configurati oppure nessun ramo, anche quando A esclude l'email. Le estrazioni esatte e la compilazione delle azioni restano in Go. B invia il confronto esclusivamente al DM Slack individuale configurato.

Per abilitare B:

1. Salvare la chiave API TypeSafe in `secrets/typesafe_api_key`, leggibile dall'UID 10001 come gli altri segreti. Il Compose standard non richiede questo file.
2. Aggiungere al bot Slack gli scope `chat:write` e `im:write` e reinstallare l'app nel workspace dopo il cambio dei permessi. Collegare o aggiornare la connessione Slack nell'applicativo.
3. Avviare con l'override opzionale:

   ```sh
   docker compose -f compose.yaml -f compose.jev.yaml config --quiet
   docker compose -f compose.yaml -f compose.jev.yaml up --build -d
   ```

4. Nell'editor del flusso spuntare **Affianca B con Jev**, descrivere in linguaggio naturale ambito, rami ed esclusioni, scegliere la connessione Slack e inserire il proprio ID membro. In Slack: profilo → **⋯ / Altro → Copia ID membro**. Sono accettati ID membro `U…`/`W…` o un DM individuale del bot `D…`; canali e gruppi sono esclusi. Il salvataggio di un flusso attivo verifica il DM senza inviare un messaggio di prova.

Per aggiornare un'installazione già avviata, seguire la procedura di aggiornamento sotto: fermare l'app e applicare anche la migrazione `002_jev_comparisons.sql`. Con B configurato, usare entrambi i file Compose anche nei comandi successivi di aggiornamento e avvio. Per il binario locale usare `TYPESAFE_API_KEY_FILE` oppure `TYPESAFE_API_KEY`; `TYPESAFE_MODEL` è facoltativo e vale `jev-latest` per impostazione iniziale.

Il DM contiene i due rami proposti, un riepilogo delle azioni, modello, confidenza, durata e consumi della risposta, con un link ai risultati completi in **Esecuzioni**. La confidenza è una stima del modello. Email, filtri e configurazione dei rami vengono trasmessi a TypeSafe: abilitarlo solo sui flussi scelti per la prova. Le chiamate possono generare costi, anche in caso di retry; i consumi mostrati riguardano la risposta salvata.

Ogni confronto conserva una copia dell'email, delle regole e del destinatario al momento della ricezione. L'analisi e l'invio hanno una coda distinta da A; un retry del report riusa il risultato salvato. Un invio Slack con esito incerto resta **Da verificare** e non viene ripetuto automaticamente. Disabilitare B o il flusso sospende i confronti ancora da avviare; riabilitarlo riprende quelli accodati con la configurazione originale. Non vengono aggiunti confronti alle esecuzioni precedenti. La simulazione RFC822 continua a verificare A senza chiamate esterne.

## Produzione e Portainer

I file per il rilascio sono in [`deploy/`](deploy/PORTAINER.md): Compose con Traefik, esempio dei segreti e guida operativa. Il workflow **Deploy production** verifica il progetto, pubblica l'immagine con tag SHA su GHCR e distribuisce su `zupit-applications`, usando l'infrastruttura di Zupit Eventi. Si avvia manualmente oppure con un tag Git `v*`; richiede l'ambiente GitHub `production` configurato.

Usare un ambiente Docker standalone e una sola replica dell'applicazione: il worker prende un lock PostgreSQL per evitare più istanze concorrenti. Portainer in modalità Swarm richiede una configurazione distinta e non può usare direttamente le dipendenze Compose per la migrazione.

- Impostare `APP_ENV=production` e `APP_BASE_URL=https://HOST_REALE`; registrare le due callback corrispondenti nel client Google.
- Terminare HTTPS sul reverse proxy aziendale. La porta pubblicata da Compose è limitata a `127.0.0.1`; un proxy in un altro container deve raggiungere `app:8080` tramite una rete Docker condivisa, da configurare per l'ambiente.
- Il Compose di produzione usa `APP_IMAGE_TAG`, fornito dalla pipeline, per la stessa immagine di `app` e `migrate`; non esegue build sul server. I segreti provengono dall'ambiente GitHub `production`. Il Compose locale continua a usare `AUTOMATIONS_IMAGE` e i file in `secrets/`.
- Tenere separati database, volume, segreti e client OAuth da zupit-eventi. Cambiare la sola password PostgreSQL nel file non aggiorna una password già salvata nel volume: una rotazione richiede anche l'aggiornamento del ruolo e della DSN.
- Prima di aggiornare: eseguire il backup del database e conservare l'immagine precedente. La pipeline ferma `app`, applica le migrazioni e attende la readiness della nuova versione. Per il rilascio manuale seguire la sequenza nella guida Portainer. Non tornare a un vecchio binario su uno schema incompatibile: ripristinare anche il relativo backup.

I token sono cifrati con AES-GCM; email, definizioni ed esecuzioni risiedono nel database e richiedono protezione del volume e dei backup. Non è ancora prevista una cancellazione automatica dello storico. Non eliminare il volume durante gli aggiornamenti.

## Regole ed esecuzioni

- Ogni flusso attivo valuta l'email indipendentemente; all'interno del flusso vince il primo ramo corrispondente. Un ramo senza condizioni è il caso finale.
- Le estrazioni regex usano RE2: la prima cattura diventa la variabile e i gruppi nominati sono disponibili per nome. Il modello key2help separa numero SR e progetto e riconosce l'URL solo dal pulsante "Accedi al ticket" convertito dal parser HTML; nel testo incollato questo URL non era presente. Configurare filtri progetto e liste; il ramo finale Foundation resta il fallback modificabile del modello. I modelli aggiornati si applicano ai nuovi flussi, non sovrascrivono quelli già salvati.
- I template Go possono usare `{{ .subject }}`, `{{ .from }}`, `{{ .to }}`, `{{ .body }}` e le variabili estratte; funzioni `trim`, `lower`, `upper`, `replace`, `urlquery`. Gli errori di parsing fermano il singolo flusso e vengono registrati.
- I mapping Jira accettano tipi semplici e JSON per campi strutturati (ad esempio componenti/etichette). I mapping Slack usano ID colonna con tipi testo, numero, checkbox, opzione, utente, data, link o JSON conforme all'API.
- La simulazione usa la definizione salvata, senza effettuare chiamate esterne o accodare azioni. Salvare le modifiche prima di simularle.
- Gmail viene elaborato sugli eventi; ogni cinque minuti un timer interno recupera eventuali notifiche perse. Il watch viene rinnovato circa ogni giorno. Non è necessario cron.
- La prima autorizzazione della casella registra un punto di passaggio; l'attivazione o riattivazione di un flusso registra un ulteriore limite. Le email precedenti non vengono rielaborate automaticamente. I checkpoint e la deduplicazione sono persistenti.
- I retry automatici sono al massimo due dopo il primo tentativo, con backoff e `Retry-After`. Le credenziali revocate sospendono la connessione. Ripristinarla permette alla coda di riprendere.
- Un timeout durante una creazione produce **Da verificare**: controllare Jira/Slack/Gmail prima di confermare un nuovo tentativo o riconciliare il riferimento esterno. Un ticket già creato ma assente dalla board o dalla coda attesa genera **Con avviso** e conserva il link; non viene ricreato. Lo stesso vale se il controllo della destinazione fallisce dopo la creazione.
- Sprint e notifiche Jira continuano a dipendere dalle automazioni Jira esistenti. L'app controlla il risultato del filtro della board oppure, per Service Management, la presenza nella coda se configurata; non sposta direttamente il ticket. I campi devono soddisfare i filtri Jira. Le azioni salvate conservano i tipi `jira` e `jira_request`; per queste ultime l'eventuale vecchio `board_id` non viene più usato.
- La durata mostrata nelle esecuzioni parte dalla registrazione nell'app, non dall'arrivo originale dell'email. Misurare separatamente la latenza completa durante il collaudo.

## Log operativi

Server e migrazioni scrivono JSON su stdout, leggibile nei log del container in Portainer o con `docker compose logs --since=10m app migrate`. Ogni evento ha un codice stabile `event`, un messaggio in italiano e gli ID necessari per collegare i passaggi. Oggetto, corpo, mittenti, token, prompt e risposte grezze dei provider non vengono scritti nei log applicativi. I riferimenti ai ticket creati e gli ID tecnici restano visibili per la diagnosi; proteggere l'accesso ai log come quello all'applicazione.

| Evento | Significato / verifica |
|---|---|
| `database.connect_failed`, `database.schema_failed` | Connessione o schema non disponibili; controllare `error.kind`, SQLSTATE, secret e servizio `migrate`. SQLSTATE `53100` indica spazio disco esaurito. |
| `flow.saved` | Flusso salvato, con `enabled` e `activated_at`; le email precedenti all'attivazione non generano esecuzioni. |
| `pubsub.started`, `pubsub.received`, `pubsub.receive_failed` | Listener avviato, notifica ricevuta o problema di trasporto; gli errori riportano il codice gRPC e indicazioni su ADC/permessi. Il recupero Gmail resta ogni 5 minuti. |
| `gmail.sync_completed`, `gmail.ingested`, `gmail.message_skipped` | Durata e candidati della sincronizzazione, email inserite, duplicati ed esclusioni. `active_flows=0` significa che nessun flusso attivo usa quella casella; `skipped_before_activation` conta le valutazioni saltate perché precedenti all'attivazione. |
| `flow.evaluated` | Esecuzione registrata dopo il commit: `run_id`, `flow_id`, `message_id` e `received_at`. `filter_mismatch` indica filtro iniziale non soddisfatto; `no_matching_branch` nessun ramo; `planning_failed` errore da aprire in Esecuzioni; `actions_queued` azioni accodate. |
| `action.started`, `action.completed` | Tentativo, connessione, tipo azione, durata, stato e riferimento esterno. `pending` include `next_at`; `warning` conserva una creazione riuscita da verificare; `uncertain` richiede riconciliazione prima di riprovare. |
| `action.persist_failed` | Il risultato esterno non è stato salvato: verificare il servizio esterno prima di ripetere l'azione. |
| `connection.paused`, `gmail.source_failed`, `connection.verify_failed` | Credenziali, autorizzazione o configurazione da correggere in Connessioni. |
| `comparison.*`, `notification.*` | Analisi Jev, consumi, invio DM e avvisi Slack, senza contenuti delle email o dei report. |

Gli errori dei provider includono lo stato HTTP e i flag `auth`, `retryable`, `uncertain`; gli errori tecnici sono classificati senza riportare stringhe che potrebbero contenere credenziali o dati. In caso di email apparentemente mancante, cercare nell'ordine connessione/listener, sincronizzazione, acquisizione, valutazione del flusso e azione. Per un'esecuzione usare gli stessi `run_id` e `step_id` nei log e nell'interfaccia.

Il Compose locale e quello Portainer limitano i log dell'app a 5 file da 10 MB. Questa rotazione non sostituisce un archivio esterno se serve conservazione più lunga e non limita la cache di build Docker.

## Verifiche e sviluppo

Go 1.27, tool Ent e templ dichiarati in `go.mod`; non servono Node o generatori globali.

```sh
make verify       # genera, controlla gofmt, test -race, vet, build
make test         # test senza race detector
make generate     # Ent e templ
make fmt          # genera e formatta tutti i package applicativi
```

Le viste sono in `internal/views`: `layout.templ` compone login, navigazione e pagine; `components.templ` contiene i controlli condivisi. Le pagine sono divise per funzionalità (`flows`, `connections`, `runs`, `simulation`); l’editor compone i componenti `flow_*` e `comparison_editor`. I dati e gli helper Go sono separati in `data.go` e `helpers.go`. Modificare i file `.templ` e rigenerare i corrispondenti `*_templ.go` con `make generate`.

I test con persistenza richiedono un PostgreSQL di test separato. Ogni test crea uno schema isolato e lo elimina a fine esecuzione; il ruolo deve poter creare schemi. Non usare il database aziendale.

```sh
export TEST_DATABASE_URL='postgres://UTENTE:PASSWORD@localhost:5432/automations_test?sslmode=disable'
make integration
```

`make integration` richiede esplicitamente la DSN ed esegue tutta la suite, inclusi OAuth e connettori simulati. Senza `TEST_DATABASE_URL`, `make test` e `make verify` saltano i test PostgreSQL. La CI avvia un database dedicato e verifica anche file generati, Compose e build Docker. Quando l'ambiente limita la cache Go, usare `GOCACHE="$PWD/.cache/go-build" make verify`.

`make migrate` e `make run` caricano ed esportano automaticamente `.env`, se presente, usando la shell; i valori nel file prevalgono sulle variabili già esportate. Usare sintassi compatibile con la shell e racchiudere tra apici i valori con spazi o caratteri speciali. Il binario Go eseguito direttamente continua a leggere solo l'ambiente.

Per avviare i binari locali serve inoltre una DSN raggiungibile dall'host: `postgres` nel file `secrets/database_url` è il nome del servizio nella rete Docker e non è raggiungibile dal processo Go locale. Il Compose standard non pubblica una porta PostgreSQL. Per usare la configurazione Docker esistente senza modificare la DSN, avviare l'applicazione con `docker compose up --build -d`; per usare `make run` occorre un PostgreSQL raggiungibile dall'host e il relativo `DATABASE_URL` o `DATABASE_URL_FILE`, poi `make migrate` e `make run`.

## Passaggio da Make

1. Raccogliere esportazioni Make ed email di ogni ramo; sostituire i segnaposto nei quattro modelli con filtri, regex, ID e destinatario PO verificati.
2. Salvare in bozza e simulare casi positivi, esclusioni SBC/Jira e casi mancanti. Controllare anche email che corrispondono a più flussi.
3. Eseguire prove su destinazioni dedicate: campi Jira/Slack, richiedente, filtro della board, automazioni sprint e notifiche; misurare la latenza completa.
4. Per un solo scenario alla volta, fermare Make, annotare ora e ultimo messaggio elaborato, attivare il nuovo flusso e riconciliare manualmente l'intervallo tra i due sistemi. Nessuno storico viene importato automaticamente.
5. Monitorare esecuzioni e canale avvisi. Se serve rollback, disattivare prima il nuovo flusso, riconciliare le email dell'intervallo e solo dopo riattivare Make. Non lasciare entrambi attivi sulla stessa destinazione.

Il collaudo con i servizi reali e il passaggio da Make restano attività da eseguire con dati e accessi aziendali.
