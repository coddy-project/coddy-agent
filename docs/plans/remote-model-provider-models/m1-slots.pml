/*
 * m1-slots.pml : the concurrency limiter of the shared-model route
 *   (docs/plans/remote-model-provider.md 4.1a, 4.2, last bullet of 4.5, rows Server/Config of 5a;
 *    scenarios "Five streams run at once and a sixth is told to retry" and
 *    "The relay's clients share the node's stream limit").
 *
 * WHAT IS MODELLED
 *   N clients, each sending one request (TRIES>1: asking again after a busy refusal) to
 *   POST /coddy/llm/completions of one node. A request is the process Req(i). The key of request i is
 *   kof[i]: two keys (A = 0 for the first NA clients, B = 1 for the rest) or, with -DONEKEY, one key for all
 *   (the relay as one caller, 4.5). Flow of one request, NEW design (the plan's text):
 *     accept -> authenticate -> TAKE SLOT (busy 429 when the key's count == LIMIT)
 *     -> Content-Length precheck (413 before any read) -> read body (can stall; body-read deadline)
 *     -> validate (400/404/413 answered, slot released at once) -> provider call (completes, provider error,
 *        model stall turned to a terminal error by StreamIdleTimeout) -> terminal event write (a client that
 *        stopped reading stalls it; per-write deadline) -> `defer` release.
 *   Environment, chosen per request by init as one of five scenarios (kind, client type): ok/normal,
 *   validation-failure/normal, Content-Length-413/normal, ok/slow-body staller (never finishes the body, never
 *   disconnects), ok/non-reader (never reads the response, never disconnects). Nondeterministic at the wait
 *   states: disconnect (normal clients), deadline expiry, provider outcome (final or error; a model stall
 *   turned into a terminal error is the same step as a provider error). Bytes are abstracted to a counter of
 *   buffered bodies per key.
 *
 * PROPERTIES: the invariants S1-S3 and the liveness L1/L2 are LTL formulas asked from the command line (see
 *   the report); the `assert`s of this file carry: busy only when the key's count == LIMIT, busy answers
 *   before any body is read, the provider is called only while the slot is held, no slot is released that is
 *   not held (double release), no slot or body is left behind when a request ends, and (init) active == 0 and
 *   no body buffered when every request is over. `deadlock` is a stuck request (every request must end).
 *
 * VARIANTS (-D switches; default = the plan: NEW, per-write deadline, body deadline, guarded model stall,
 *           two keys, atomic take, one release per exit path)
 *   OLD           slot taken only after the body is read and validated (the rejected design)
 *   NOWRITEDL     no per-write deadline        NOBODYDL   no body-read deadline
 *   ONEKEY        all clients share one key (the relay)
 *   UNGUARDED     a stream:false row: a model stall is not turned into an error (only a disconnect ends it)
 *   NO_SLOWBODY / NO_NOREAD   drop the staller / the non-reader from the environment
 *   CLOCK         the deadlines of the stallers are explicit discrete-time countdowns of D ticks with a Tick
 *                 process (expiry urgent) instead of an always-enabled expiry alternative
 *   GHOST         add the ghost counters used by the reach properties (kept out of the safety runs: they
 *                 multiply states). Not meant for OLD (the staller counter assumes the NEW slot order).
 *   mutations (anti-vacuity): MUT_HIGH (take when count <= LIMIT), MUT_EARLY (busy already at LIMIT-1),
 *     MUT_SHARED (one count for all keys), MUT_NONATOMIC (check and increment are two steps),
 *     MUT_DOUBLEREL (the 4xx path releases and the defer releases again), LEAK=p (exit path p forgets to
 *     release: 1 complete, 2 provider error, 3 disconnect, 4 body timeout, 5 4xx, 6 write timeout),
 *     MUT_BODYAFTER (body dropped one step after the slot is released).
 *
 * CONSTANTS: LIMIT (2 and 3; the plan's default is 5), N clients (4), NA clients on key A (3), TRIES (1;
 *   2 for the retry reach), D ticks of a deadline under CLOCK (2).
 *
 * ABSTRACTIONS (declared in the report)
 *   - TIME. The engine is untimed. Default: a staller's deadline is an always-enabled "expire" alternative in
 *     the state where it is armed (it may fire at any moment: a superset of the real behaviours, which only
 *     admits earlier expiry); a normal client may always time out spuriously. With CLOCK the staller's
 *     deadline is a countdown decremented by a Tick process, expiry urgent. Neither is a timed automaton.
 *   - Steps are merged where they touch only local variables (they commute with every other process); the
 *     visible actions are take, "body buffered", release+drop and the arm/FIRED of CLOCK. take and release
 *     are single atomic steps (the real code needs a mutex or CAS; MUT_NONATOMIC shows why).
 *   - The body buffer is dropped in the same step as the release (live memory of a handler holding a slot;
 *     garbage awaiting a GC cycle is not modelled).
 *   - Error answers (413/400/404/429) are tiny and never block; a chunk or terminal write blocks only for a
 *     non-reader (`final` carries the whole response and can exceed the socket buffers). The 15 s
 *     heartbeat writes are not modelled (they only add places where a non-reader is cut). A chunk write
 *     before the outcome is subsumed by the terminal write: for a non-reader both stall the same way.
 *   - Symmetry: the clients of one key are interchangeable and init picks their scenarios in non-decreasing
 *     order (hand-made symmetry reduction). Every property asked is invariant under permuting the clients of a
 *     key; a property about one named client is not asked. The key of a client is fixed at start.
 *   - No infinite behaviour: a request ends, so liveness reduces to "no request is stuck while holding a slot";
 *     the engine's weak fairness is therefore not needed for the verdicts (and does not rescue a stuck one).
 *   - The counters active and bodies are signed (short) so that a double release shows as a wrong count and
 *     not as a byte-domain error.
 *
 * REPRODUCE (examples; the report has the full list):
 *   mcd check --promela m1-slots.pml -D N=4 --max-procs 5 --ltl '[] (active[0] <= 2 && active[1] <= 2)' ...
 *   mcd check --promela m1-slots.pml -D N=3 -D ONEKEY -D NOWRITEDL --max-procs 5 --ltl '...L1...'
 */

#ifndef LIMIT
#define LIMIT 2
#endif
#ifndef N
#define N 4
#endif
#ifndef NA
#define NA 3
#endif
#ifndef TRIES
#define TRIES 1
#endif
#ifndef LEAK
#define LEAK 0
#endif
#ifndef D
#define D 2
#endif

/* request kinds */
#define K_OK    0
#define K_VAL   1   /* 400, 404 or a body-length 413: answered after the body is read */
#define K_CL413 2   /* Content-Length above the limit: 413 before any read */
/* client types */
#define C_NORMAL   0
#define C_SLOWBODY 1  /* stops sending the body, never disconnects */
#define C_NOREAD   2  /* sends the body, never reads the response, never disconnects */
/* exit paths: the release must happen exactly once on every one that holds a slot */
#define P_COMPLETE 1
#define P_PROVERR  2
#define P_DISC     3
#define P_BODYTO   4
#define P_4XX      5
#define P_WRTO     6
/* the process states */
#define ST_ATT   0   /* attempt: environment, (disconnect | take the slot) */
#define ST_INC   1   /* MUT_NONATOMIC only: the increment after the check */
#define ST_BODY  2   /* slot held: Content-Length precheck, start reading the body */
#define ST_RD    3   /* waiting for the body */
#define ST_PROV  4   /* provider call running, stream held open */
#define ST_WR    5   /* a non-reader's write is blocked */
#define ST_HANG  6   /* UNGUARDED: the model stalled, nothing ends it but a disconnect */
#define ST_DROP  7   /* MUT_BODYAFTER only */
#define ST_DONE  8
/* the provider's result */
#define W_FINAL 1
#define W_ERROR 2

byte kof[N];        /* key of request i */
short active[2];    /* the limiter under test: running calls per key (signed: a double release shows as a negative count, not a domain error) */
byte held[N];       /* ghost: 1 while request i owns a slot */
byte heldA[N];      /* ghost: owns a slot of key 0 */
byte heldB[N];      /* ghost: owns a slot of key 1 */
short bodies[2];    /* buffered request bodies per key (the abstraction of bytes) */
bit  bufd[N];       /* ghost: request i has a body buffered */
bit  rdstart[N];    /* ghost: request i began to read its body */
byte takes[N];
byte rels[N];
byte finished;
byte cd[N];         /* CLOCK: remaining ticks of the armed deadline of request i */
bit  armed[N];
#ifdef GHOST
byte nbusy;         /* busy refusals so far */
byte ncomplete;     /* streams that ended with a written terminal event */
bit  pr[7];         /* some request released through exit path p */
bit  servedafterbusy;
bit  cl413rel;      /* a Content-Length 413 released its slot before any body was read */
bit  busyfull;      /* a busy refusal while LIMIT calls of that key were all running in the provider */
bit  busystall;     /* a busy refusal while LIMIT slots of that key were all pinned by slow-body stallers */
byte nstall[2];     /* slow-body stallers of key k currently holding a slot while their body is awaited */
byte nprov[2];      /* calls of key k currently in the provider phase */
#define GH_PR(p)   pr[p] = 1
#define GH_BUSY    nbusy++; if :: nprov[k] == LIMIT -> busyfull = 1 :: else -> skip fi; if :: nstall[k] == LIMIT -> busystall = 1 :: else -> skip fi;
#define GH_STIN(k)  if :: ct == C_SLOWBODY -> nstall[k]++ :: else -> skip fi;
#define GH_STOUT(k) nstall[k]--;
#define GH_TOPROV(k)   nprov[k]++;
#define GH_FROMPROV(k) nprov[k]--;
#define GH_CL      cl413rel = 1;
#define GH_DONE(h) ncomplete++; if :: h -> servedafterbusy = 1 :: else -> skip fi
#else
#define GH_PR(p)   skip
#define GH_BUSY
#define GH_TOPROV(k)   skip;
#define GH_FROMPROV(k)
#define GH_CL
#define GH_STIN(k)  skip;
#define GH_STOUT(k)
#define GH_DONE(h) skip
#endif

#if N == 3
#define HA (heldA[0] + heldA[1] + heldA[2])
#define HB (heldB[0] + heldB[1] + heldB[2])
#elif N == 4
#define HA (heldA[0] + heldA[1] + heldA[2] + heldA[3])
#define HB (heldB[0] + heldB[1] + heldB[2] + heldB[3])
#elif N == 5
#define HA (heldA[0] + heldA[1] + heldA[2] + heldA[3] + heldA[4])
#define HB (heldB[0] + heldB[1] + heldB[2] + heldB[3] + heldB[4])
#elif N == 6
#define HA (heldA[0] + heldA[1] + heldA[2] + heldA[3] + heldA[4] + heldA[5])
#define HB (heldB[0] + heldB[1] + heldB[2] + heldB[3] + heldB[4] + heldB[5])
#endif

/* room for one more call on key k */
#if defined(MUT_HIGH)
#define ROOM(k) (active[k] <= LIMIT)
#elif defined(MUT_EARLY)
#define ROOM(k) (active[k] + 1 < LIMIT)
#elif defined(MUT_SHARED)
#define ROOM(k) (active[0] + active[1] < LIMIT)
#else
#define ROOM(k) (active[k] < LIMIT)
#endif

/* deadlines of the stallers: arm when the wait starts, FIRED guards the expiry, disarm when it ends */
#ifdef CLOCK
inline arm(i)    { atomic { armed[i] = 1; cd[i] = D } }
inline disarm(i) { atomic { armed[i] = 0; cd[i] = 0 } }
#define FIRED(i) (armed[i] && cd[i] == 0)
#else
inline arm(i)    { skip }
inline disarm(i) { skip }
#define FIRED(i) true
#endif

/* the slot is counted */
inline count_in(i, k) {
	active[k]++;
	held[i] = 1;
	if :: k == 0 -> heldA[i] = 1 :: else -> heldB[i] = 1 fi;
	takes[i]++
}

/* the handler has returned: nothing held, every take matched by one release, no body kept */
inline fin_check(i) {
	assert(held[i] == 0);
	assert(takes[i] == rels[i]);
	assert(bufd[i] == 0);
	finished++;
	takes[i] = 0; rels[i] = 0; rdstart[i] = 0       /* forget the history of a finished request (state-space) */
}

inline dropbody(i, k) {
	if :: bufd[i] -> bodies[k]--; bufd[i] = 0 :: else -> skip fi
}

/* leave a handler that holds a slot (the `defer`, or the 4xx answer): drop the body, give the slot back once */
#ifdef MUT_BODYAFTER
inline exit_slot(i, k, path, st) {
	atomic {
		assert(held[i] == 1);
		if
		:: LEAK != path -> held[i] = 0; heldA[i] = 0; heldB[i] = 0; active[k]--; rels[i]++; GH_PR(path)
		:: else -> skip
		fi;
		st = ST_DROP
	}
}
#else
inline exit_slot(i, k, path, st) {
	atomic {
		assert(held[i] == 1);                 /* releasing a slot that is not held = a double release */
		dropbody(i, k);
		if
		:: LEAK != path -> held[i] = 0; heldA[i] = 0; heldB[i] = 0; active[k]--; rels[i]++; GH_PR(path)
		:: else -> skip                       /* mutation: this exit path forgets to release */
		fi;
#ifdef MUT_DOUBLEREL
		if
		:: path == P_4XX ->                   /* the 4xx answer released and the defer releases again */
			assert(held[i] == 1);
			active[k]--
		:: else -> skip
		fi;
#endif
		fin_check(i);
		break
	}
}
#endif

/* leave a handler that holds nothing (before the slot, or OLD after its body was read) */
inline exit_noslot(i, k, st) {
	atomic {
		assert(held[i] == 0);
		dropbody(i, k);
		fin_check(i);
		break
	}
}

/* the decision at the limiter: one more call on key k, or busy */
inline take_or_busy(i, k, st, tries, hadbusy, nxt) {
#ifdef MUT_NONATOMIC
	if
	:: ROOM(k) -> st = ST_INC                     /* checked; the increment is a later step */
#else
	if
	:: ROOM(k) -> count_in(i, k); st = nxt; if :: nxt == ST_PROV -> GH_TOPROV(k) :: else -> skip fi
#endif
	:: else ->
		assert(active[k] == LIMIT);           /* busy only when the key's count == LIMIT at the decision */
		assert(rdstart[i] == 0);              /* busy answers without having read the body */
		GH_BUSY
		hadbusy = 1;
		if
		:: tries + 1 < TRIES -> dropbody(i, k); tries++; st = ST_ATT      /* the client waits it out and asks again */
		:: else -> exit_noslot(i, k, st)
		fi
	fi
}

#ifdef OLD
#define EXIT_RD(path) exit_noslot(i, k, st)
#else
#define EXIT_RD(path) exit_slot(i, k, path, st)
#endif

inline armrd(i, ct) {
	if :: ct == C_SLOWBODY -> arm(i) :: else -> skip fi
}

proctype Req(byte i; byte kind; byte ct) {
	byte k; byte tries; byte st = ST_ATT; byte path; byte wres; bit hadbusy;
	do
	:: atomic { st == ST_ATT ->
		k = kof[i];
		rdstart[i] = 0;
		if
		:: ct == C_NORMAL -> exit_noslot(i, k, st)        /* leaves before holding anything */
		:: true ->
#ifdef OLD
			if
			:: kind == K_CL413 -> exit_noslot(i, k, st)   /* 413 before any read */
			:: else ->
				bodies[k]++; bufd[i] = 1; rdstart[i] = 1;
				armrd(i, ct);
				st = ST_RD                        /* body read starts, no slot held */
			fi
#else
			take_or_busy(i, k, st, tries, hadbusy, ST_BODY)
#endif
		fi
	}
#ifdef MUT_NONATOMIC
	:: atomic { st == ST_INC -> count_in(i, k); st = ST_BODY }
#endif
#ifndef OLD
	:: atomic { st == ST_BODY ->
		if
		:: kind == K_CL413 -> path = P_4XX; GH_CL exit_slot(i, k, path, st)   /* 413 before any read, slot released at once */
		:: else ->
			assert(kind != K_CL413);
			bodies[k]++; bufd[i] = 1; rdstart[i] = 1;
			GH_STIN(k)
			armrd(i, ct);
			st = ST_RD
		fi
	}
#endif
	/* waiting for the body */
	:: atomic { st == ST_RD && ct != C_SLOWBODY ->          /* the body is complete */
		disarm(i);
		if
		:: kind == K_VAL -> path = P_4XX; EXIT_RD(path)        /* 400 / 404 / 413: answered, slot released */
		:: else ->
#ifdef OLD
			take_or_busy(i, k, st, tries, hadbusy, ST_PROV)
#else
			GH_TOPROV(k)
			st = ST_PROV
#endif
		fi
	}
	:: atomic { st == ST_RD && ct == C_NORMAL -> disarm(i); path = P_DISC; EXIT_RD(path) }     /* client gone */
#ifndef NOBODYDL
	:: atomic { st == ST_RD && ct == C_NORMAL -> disarm(i); path = P_BODYTO; EXIT_RD(path) }   /* slow live client cut */
	:: atomic { st == ST_RD && ct == C_SLOWBODY && FIRED(i) -> GH_STOUT(k) disarm(i); path = P_BODYTO; EXIT_RD(path) }
#endif
	/* the provider call is running, the stream is held open */
	:: atomic { st == ST_PROV ->
		assert(held[i] == 1);                                  /* nothing goes to the provider without a slot */
		GH_FROMPROV(k)
		if :: wres = W_FINAL :: wres = W_ERROR fi;             /* it completes, or fails / stalls into a terminal error */
		if
		:: ct == C_NOREAD -> arm(i); st = ST_WR                /* the terminal write blocks */
		:: else ->
			if
			:: wres == W_FINAL -> path = P_COMPLETE; GH_DONE(hadbusy)
			:: else -> path = P_PROVERR
			fi;
			exit_slot(i, k, path, st)
		fi
	}
	:: atomic { st == ST_PROV && ct == C_NORMAL -> GH_FROMPROV(k) path = P_DISC; exit_slot(i, k, path, st) }   /* client gone: upstream cancelled */
#ifndef NOWRITEDL
	:: atomic { st == ST_PROV && ct == C_NORMAL -> GH_FROMPROV(k) path = P_WRTO; exit_slot(i, k, path, st) }   /* slow reader cut by the write deadline */
#endif
#ifdef UNGUARDED
	:: st == ST_PROV -> st = ST_HANG                          /* stream:false: a model stall is not guarded */
	:: atomic { st == ST_HANG && ct == C_NORMAL -> GH_FROMPROV(k) path = P_DISC; exit_slot(i, k, path, st) }
#endif
#ifndef NOWRITEDL
	:: atomic { st == ST_WR && FIRED(i) -> disarm(i); path = P_WRTO; exit_slot(i, k, path, st) }
#endif
#ifdef MUT_BODYAFTER
	:: atomic { st == ST_DROP -> dropbody(i, k); fin_check(i); break }
#endif
	od
}

#ifdef CLOCK
/* time passes only while a deadline is armed; an armed timer that reached 0 is handled before more time passes */
#if N == 3
#define TICK_RUN (armed[0] && cd[0] > 0 || armed[1] && cd[1] > 0 || armed[2] && cd[2] > 0)
#define TICK_EXP (armed[0] && cd[0] == 0 || armed[1] && cd[1] == 0 || armed[2] && cd[2] == 0)
#elif N == 4
#define TICK_RUN (armed[0] && cd[0] > 0 || armed[1] && cd[1] > 0 || armed[2] && cd[2] > 0 || armed[3] && cd[3] > 0)
#define TICK_EXP (armed[0] && cd[0] == 0 || armed[1] && cd[1] == 0 || armed[2] && cd[2] == 0 || armed[3] && cd[3] == 0)
#endif
active proctype Tick() {
	byte j;
end:	do
	:: atomic {
		TICK_RUN && !TICK_EXP ->
		j = 0;
		do
		:: j < N -> if :: armed[j] && cd[j] > 0 -> cd[j]-- :: else -> skip fi; j++
		:: else -> break
		od
	}
	od
}
#endif

/* the scenarios a client can be (kind of request, type of client); kind is irrelevant for the stallers */
#define SC_OK    0   /* K_OK,    C_NORMAL   */
#define SC_VAL   1   /* K_VAL,   C_NORMAL   */
#define SC_CL    2   /* K_CL413, C_NORMAL   */
#define SC_SLOW  3   /* K_OK,    C_SLOWBODY */
#define SC_NOREAD 4  /* K_OK,    C_NOREAD   */
#if defined(NO_SLOWBODY) && defined(NO_NOREAD)
#define NSC 3
#elif defined(NO_SLOWBODY) || defined(NO_NOREAD)
#define NSC 4
#else
#define NSC 5
#endif

/* Clients of one key are interchangeable: their scenarios are chosen in non-decreasing order
 * (symmetry reduction by hand; every property below is invariant under permuting the clients of a key,
 * so none is lost, but a property about one named client is not asked). */
init {
	byte i = 0; byte sc = 0; byte prev = 0; byte kd; byte cl;
	atomic {
		do
		:: i < N ->
#ifdef ONEKEY
			kof[i] = 0;
#else
			if :: i < NA -> kof[i] = 0 :: else -> kof[i] = 1 fi;
#endif
			if
			:: i == 0 -> prev = 0
			:: else -> if :: kof[i] != kof[i - 1] -> prev = 0 :: else -> skip fi
			fi;
			sc = prev;
			do :: sc < NSC - 1 -> sc++ :: break od;
			prev = sc;
			if
			:: sc == SC_OK -> kd = K_OK; cl = C_NORMAL
			:: sc == SC_VAL -> kd = K_VAL; cl = C_NORMAL
			:: sc == SC_CL -> kd = K_CL413; cl = C_NORMAL
#if !defined(NO_SLOWBODY)
			:: sc == SC_SLOW -> kd = K_OK; cl = C_SLOWBODY
#endif
#if !defined(NO_NOREAD) && !defined(NO_SLOWBODY)
			:: sc == SC_NOREAD -> kd = K_OK; cl = C_NOREAD
#endif
#if !defined(NO_NOREAD) && defined(NO_SLOWBODY)
			:: sc == SC_SLOW -> kd = K_OK; cl = C_NOREAD
#endif
			fi;
			run Req(i, kd, cl);
			i++
		:: else -> break
		od
	};
	/* when every request is over nothing is held and nothing is buffered */
	finished == N;
	assert(active[0] == 0 && active[1] == 0);
	assert(bodies[0] == 0 && bodies[1] == 0)
}
