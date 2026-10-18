/* One operator command request (internal/acp/operator_command.go,
 * internal/tools/shell/operator_tool.go): two followers of the session answer
 * run / decline / stop in any order and any number of times, the tool waits
 * for the decision or the turn's cancel, runs the command, applies Stop
 * (interrupt, escalation after the grace, a second stop kills), and ends the
 * request. The command may end by itself, may ignore the interrupt, and dies
 * on a kill. Ticket.Answer is one critical section in Go: atomic here. */

#define WAITING  0
#define RUNNING  1
#define STOPPING 2
#define KILLING  3
#define DECLINED 4
#define ENDED    5

byte st = WAITING;      /* ticket state */
bool decided = false;   /* the decided channel is closed */
bool stopc = false;     /* Stop closed */
bool killc = false;     /* Kill closed */
bool cancelled = false; /* the turn's context ended */

byte started = 0;       /* how many times the command was started */
bool ranAnswer = false; /* a run answer was accepted */
bool alive = false;     /* the command's process group exists */
bool interrupted = false;
bool toolDone = false;  /* the tool call returned */
bool killerPending = false; /* the delayed terminate of a cancelled turn is on its way */

/* Ticket.Answer for one action; a refused answer changes nothing. */
inline answer(a) {
  atomic {
    if
    :: a == 1 && st == WAITING  -> st = RUNNING; decided = true; ranAnswer = true
    :: a == 2 && st == WAITING  -> st = DECLINED; decided = true
    :: a == 3 && st == RUNNING  -> st = STOPPING; stopc = true
    :: a == 3 && st == STOPPING -> st = KILLING; killc = true
    :: else -> skip
    fi
  }
}

active [2] proctype Follower() {
  byte n = 0;
  do
  :: n < 3 -> if
              :: answer(1)
              :: answer(2)
              :: answer(3)
              fi; n++
  :: n < 3 -> n = 3   /* walks away */
  :: n == 3 -> break
  od
}

active proctype Turn() {
  if
  :: cancelled = true
  :: skip
  fi
}

active proctype Command() {
  /* waits to be started, then ends by itself, on an interrupt it does not
   * ignore, or on a kill */
end_idle:
  (alive);
  do
  :: !alive -> break
  :: alive -> atomic { alive = false }          /* exits by itself */
  :: alive -> skip                              /* keeps running */
  :: alive && interrupted -> atomic { alive = false } /* honours Ctrl+C */
  :: alive && interrupted -> skip               /* ignores it */
  od
}

/* The goroutine a cancel starts: after the interrupt's grace it terminates
 * the group (operator_tool.go, the ctx.Done case). */
active proctype Killer() {
end_k:
  (killerPending && alive) -> alive = false
}

active proctype Tool() {
  bool escalated = false;
  bool killed = false;   /* kill = nil after the first receive, as in Go */
  /* Ticket.Wait: no timeout by design - a request nobody answers waits
   * until the turn is cancelled */
end_wait:
  if
  :: decided -> skip
  :: cancelled && !decided ->
       atomic {
         if
         :: st == WAITING -> st = ENDED; goto out
         :: else -> skip   /* the decision landed at the same moment */
         fi
       }
  fi;
  if
  :: st == DECLINED -> goto out
  :: else -> skip
  fi;
  /* run */
  atomic { started++; alive = true };
  do
  :: !alive -> break
  :: stopc && !interrupted -> interrupted = true      /* Interrupt */
  :: interrupted && !escalated -> escalated = true; alive = false /* grace ran out: terminate */
  :: killc && !killed -> killed = true; alive = false /* second stop: kill */
  :: cancelled && alive -> interrupted = true; killerPending = true;
       /* the turn waits for the reap at most operatorCancelDrain */
       if :: (!alive) :: skip /* the drain expired */ fi; break
  od;
  toolDone = true;
  /* superviseOperatorRunAndRelease: the request ends once the process has */
end_release:
  (!alive);
  atomic { if :: st != ENDED -> st = ENDED :: else -> skip fi };
  goto fin;
out:
  toolDone = true;
fin:
  skip
}
