# Zupit Automazioni

Servizio Go per sostituire gli scenari Make: Gmail → condizioni/estrazioni → Jira Cloud, Slack List o inoltro email. Interfaccia Chi/templ/HTMX, login Google Workspace e coda persistente PostgreSQL/Ent. Il piano concordato è conservato in [PLAN.md](PLAN.md).

## Stato

Sono implementati login e ruoli, connessioni cifrate, editor, simulazione RFC822, ricezione Pub/Sub StreamingPull, recupero Gmail, coda, retry, riconciliazione manuale e avvisi Slack. I quattro modelli SRT, CUP, key2help e Support KR si aprono da **Flussi** come bozze; scegliere una sorgente Gmail per salvarli. Tutti i valori `DA_CONFIGURARE` devono essere sostituiti prima dell'attivazione.

Sono disponibili esempi del contenuto delle notifiche SRT e key2help e del risultato CUP già creato da Make/Jira. I [campioni di test](examples/README.md) usano intestazioni sintetiche e dati adattati; mancano l'email sorgente CUP, le intestazioni originali, le esportazioni Make, le credenziali e i mapping definitivi. I modelli non sono scenari pronti per la produzione. Scritture reali, board/sprint Jira e latenza dall'arrivo dell'email richiedono collaudo sulle destinazioni aziendali.

## Configurazione Google e destinazioni

1. Nel progetto Google Cloud abilitare Gmail API e Pub/Sub API. Creare un client OAuth **Web** con consenso interno all'organizzazione e queste callback esatte:
   - `APP_BASE_URL/auth/callback` per il login;
   - `APP_BASE_URL/auth/gmail/callback` per collegare una casella.
2. Autorizzare gli scope `openid`, `email`, `profile`, `gmail.readonly` e `gmail.send`. L'app richiede gli scope Gmail solo durante il collegamento della casella; usare una casella Workspace reale, anche diversa dall'account amministratore.
3. Creare un topic nel medesimo progetto del client OAuth e una subscription **pull**. Dare a `gmail-api-push@system.gserviceaccount.com` il ruolo Publisher sul topic. Fornire al server credenziali ADC con ruolo Subscriber sulla subscription. Non è necessario un webhook Pub/Sub pubblico. [Configurazione Gmail push](https://developers.google.com/workspace/gmail/api/guides/push).
4. Per Jira usare un account tecnico con API token: accesso a progetto e board, creazione ticket, campi e modifica del richiedente quando richiesta. Impostare progetto, tipo ticket, board e account ID del richiedente; per Service Management usare service desk e tipo richiesta. Il controllo delle destinazioni all'attivazione è in sola lettura; verificare anche i permessi di scrittura con un ticket di prova.
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

Usare un ambiente Docker standalone e una sola replica dell'applicazione: il worker prende un lock PostgreSQL per evitare più istanze concorrenti. Portainer in modalità Swarm richiede una configurazione distinta e non può usare direttamente le dipendenze Compose per la migrazione.

- Impostare `APP_ENV=production` e `APP_BASE_URL=https://HOST_REALE`; registrare le due callback corrispondenti nel client Google.
- Terminare HTTPS sul reverse proxy aziendale. La porta pubblicata da Compose è limitata a `127.0.0.1`; un proxy in un altro container deve raggiungere `app:8080` tramite una rete Docker condivisa, da configurare per l'ambiente.
- Costruire e pubblicare l'immagine nel proprio registry, impostare `AUTOMATIONS_IMAGE` a un tag immutabile e usare la stessa immagine per `app` e `migrate`. In Portainer rimuovere `build` dall'ancora `x-image` quando si distribuisce l'immagine già costruita. I percorsi dei segreti devono esistere sull'host dell'ambiente Docker.
- Tenere separati database, volume, segreti e client OAuth da zupit-eventi. Cambiare la sola password PostgreSQL nel file non aggiorna una password già salvata nel volume: una rotazione richiede anche l'aggiornamento del ruolo e della DSN.
- Prima di aggiornare: fermare `app`, eseguire il backup del database e conservare l'immagine precedente. Poi applicare le migrazioni con `docker compose run --rm migrate` e avviare `app`. Non tornare a un vecchio binario su uno schema incompatibile: ripristinare anche il relativo backup.

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
- Un timeout durante una creazione produce **Da verificare**: controllare Jira/Slack/Gmail prima di confermare un nuovo tentativo o riconciliare il riferimento esterno. Un ticket già creato con board errata genera **Con avviso** e conserva il link; non viene ricreato.
- Sprint e notifiche Jira continuano a dipendere dalle automazioni Jira esistenti. L'app controlla il risultato del filtro della board; non sposta direttamente il ticket in una board.
- La durata mostrata nelle esecuzioni parte dalla registrazione nell'app, non dall'arrivo originale dell'email. Misurare separatamente la latenza completa durante il collaudo.

## Verifiche e sviluppo

Go 1.27, tool Ent e templ dichiarati in `go.mod`; non servono Node o generatori globali.

```sh
make verify       # genera, controlla gofmt, test -race, vet, build
make test         # test senza race detector
make generate     # Ent e templ
make fmt          # genera e formatta tutti i package applicativi
```

I test con persistenza richiedono un PostgreSQL di test separato. Ogni test crea uno schema isolato e lo elimina a fine esecuzione; il ruolo deve poter creare schemi. Non usare il database aziendale.

```sh
export TEST_DATABASE_URL='postgres://UTENTE:PASSWORD@localhost:5432/automations_test?sslmode=disable'
make integration
```

`make integration` richiede esplicitamente la DSN ed esegue tutta la suite, inclusi OAuth e connettori simulati. Senza `TEST_DATABASE_URL`, `make test` e `make verify` saltano i test PostgreSQL. La CI avvia un database dedicato e verifica anche file generati, Compose e build Docker. Quando l'ambiente limita la cache Go, usare `GOCACHE="$PWD/.cache/go-build" make verify`.

Per avviare i binari locali, esportare le variabili di `.env` (il programma non carica file dotenv), usare una DSN raggiungibile dall'host e poi `make migrate` e `make run`. Il database Compose non pubblica una porta sull'host.

## Passaggio da Make

1. Raccogliere esportazioni Make ed email di ogni ramo; sostituire i segnaposto nei quattro modelli con filtri, regex, ID e destinatario PO verificati.
2. Salvare in bozza e simulare casi positivi, esclusioni SBC/Jira e casi mancanti. Controllare anche email che corrispondono a più flussi.
3. Eseguire prove su destinazioni dedicate: campi Jira/Slack, richiedente, filtro della board, automazioni sprint e notifiche; misurare la latenza completa.
4. Per un solo scenario alla volta, fermare Make, annotare ora e ultimo messaggio elaborato, attivare il nuovo flusso e riconciliare manualmente l'intervallo tra i due sistemi. Nessuno storico viene importato automaticamente.
5. Monitorare esecuzioni e canale avvisi. Se serve rollback, disattivare prima il nuovo flusso, riconciliare le email dell'intervallo e solo dopo riattivare Make. Non lasciare entrambi attivi sulla stessa destinazione.

Il collaudo con i servizi reali e il passaggio da Make restano attività da eseguire con dati e accessi aziendali.
