import Ecto.Query
alias Plausible.{Repo, Auth}

user = Repo.one!(from u in Auth.User, where: u.email == "admin@bench.test")
team = Repo.one!(
  from t in Plausible.Teams.Team,
    join: m in Plausible.Teams.Membership,
    on: m.team_id == t.id,
    where: m.user_id == ^user.id,
    limit: 1
)

{:ok, _} = Auth.create_stats_api_key(user, team, "accuracy-ab", "__ACCURACY_AB_KEY__")
