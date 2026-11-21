import datetime
import uuid

import grpc
import jwt
import pytest

import demo_pb2
from grpc_health.v1 import health_pb2
import server
from user_store import UserAlreadyExists, UserNotFound

SECRET = 'x' * 32


class Aborted(Exception):
    def __init__(self, code, details):
        super().__init__(f"{code}: {details}")
        self.code = code
        self.details = details


class FakeContext:
    def abort(self, code, details):
        raise Aborted(code, details)


class FakeStore:
    def __init__(self):
        self.users = {}
        self.orders = {}
        self.healthy = True
        self._tick = 0

    def create_user(self, email, name, password_hash):
        if email in self.users:
            raise UserAlreadyExists()
        user = {'id': uuid.uuid4(), 'email': email, 'name': name, 'password_hash': password_hash}
        self.users[email] = user
        return {k: user[k] for k in ('id', 'email', 'name')}

    def get_user_by_email(self, email):
        return self.users.get(email.lower())

    def get_user_by_id(self, user_id):
        for u in self.users.values():
            if str(u['id']) == user_id:
                return {k: u[k] for k in ('id', 'email', 'name')}
        return None

    def ping(self):
        return self.healthy

    def record_order(self, order_id, user_id, total, order_data):
        if not any(str(u['id']) == user_id for u in self.users.values()):
            raise UserNotFound(user_id)
        if order_id in self.orders:
            return False
        self._tick += 1
        currency, units, nanos = total
        self.orders[order_id] = {
            'id': order_id, 'user_id': user_id, 'total_currency': currency,
            'total_units': units, 'total_nanos': nanos, 'order_data': order_data,
            'created_at': datetime.datetime(2026, 1, 1, tzinfo=datetime.timezone.utc)
                          + datetime.timedelta(minutes=self._tick),
        }
        return True

    def list_orders(self, user_id):
        rows = [o for o in self.orders.values() if o['user_id'] == user_id]
        return sorted(rows, key=lambda o: o['created_at'], reverse=True)


class FakeClock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now


@pytest.fixture
def store():
    return FakeStore()


@pytest.fixture
def clock():
    return FakeClock()


@pytest.fixture
def svc(store, clock):
    return server.UserServicer(store, SECRET, server.LoginThrottle(max_failures=3, window_s=60, clock=clock))


def register(svc, email='Alice@Example.com', password='correct-horse', name='Alice'):
    return svc.Register(demo_pb2.RegisterRequest(email=email, password=password, name=name), FakeContext())


def login(svc, email='alice@example.com', password='correct-horse'):
    return svc.Login(demo_pb2.LoginRequest(email=email, password=password), FakeContext())


def decode(token):
    return jwt.decode(token, SECRET, algorithms=['HS256'], audience='frontend', issuer='userservice')


def test_register_returns_valid_token_and_normalizes_email(svc, store):
    resp = register(svc)
    assert 'alice@example.com' in store.users
    claims = decode(resp.jwt_token)
    assert claims['sub'] == resp.user_id
    assert claims['email'] == 'alice@example.com'


@pytest.mark.parametrize('kwargs', [
    {'email': ''},
    {'email': 'not-an-email'},
    {'password': 'short'},
    {'password': 'x' * 73},
    {'name': '   '},
])
def test_register_rejects_invalid_input(svc, kwargs):
    with pytest.raises(Aborted) as e:
        register(svc, **kwargs)
    assert e.value.code == grpc.StatusCode.INVALID_ARGUMENT


def test_register_duplicate_email_is_already_exists(svc):
    register(svc)
    with pytest.raises(Aborted) as e:
        register(svc, email='ALICE@example.com')
    assert e.value.code == grpc.StatusCode.ALREADY_EXISTS


def test_login_success_is_case_insensitive(svc):
    reg = register(svc)
    resp = login(svc, email='  ALICE@example.com ')
    assert resp.user_id == reg.user_id
    assert resp.name == 'Alice'
    assert decode(resp.jwt_token)['sub'] == reg.user_id


def test_login_unknown_email_and_wrong_password_look_identical(svc):
    register(svc)
    with pytest.raises(Aborted) as unknown:
        login(svc, email='bob@example.com')
    with pytest.raises(Aborted) as wrong:
        login(svc, password='wrong-password')
    assert unknown.value.code == wrong.value.code == grpc.StatusCode.UNAUTHENTICATED
    assert unknown.value.details == wrong.value.details


def test_login_is_throttled_after_repeated_failures(svc, clock):
    register(svc)
    for _ in range(3):
        with pytest.raises(Aborted):
            login(svc, password='wrong-password')
    # Even the correct password is refused while locked out.
    with pytest.raises(Aborted) as e:
        login(svc)
    assert e.value.code == grpc.StatusCode.RESOURCE_EXHAUSTED

    clock.now += 61
    assert login(svc).user_id


def test_successful_login_resets_failures(svc):
    register(svc)
    for _ in range(2):
        with pytest.raises(Aborted):
            login(svc, password='wrong-password')
    login(svc)
    for _ in range(2):
        with pytest.raises(Aborted) as e:
            login(svc, password='wrong-password')
        assert e.value.code == grpc.StatusCode.UNAUTHENTICATED


def test_get_user(svc):
    reg = register(svc)
    resp = svc.GetUser(demo_pb2.GetUserRequest(user_id=reg.user_id), FakeContext())
    assert resp.email == 'alice@example.com'
    with pytest.raises(Aborted) as e:
        svc.GetUser(demo_pb2.GetUserRequest(user_id='nope'), FakeContext())
    assert e.value.code == grpc.StatusCode.INVALID_ARGUMENT
    with pytest.raises(Aborted) as e:
        svc.GetUser(demo_pb2.GetUserRequest(user_id=str(uuid.uuid4())), FakeContext())
    assert e.value.code == grpc.StatusCode.NOT_FOUND


def test_health_liveness_ignores_db_but_readiness_does_not(store):
    health = server.HealthServicer(store)
    Status = health_pb2.HealthCheckResponse
    store.healthy = False
    live = health.Check(health_pb2.HealthCheckRequest(service=''), FakeContext())
    ready = health.Check(health_pb2.HealthCheckRequest(service=server.SERVICE_NAME), FakeContext())
    assert live.status == Status.SERVING
    assert ready.status == Status.NOT_SERVING


def test_jwt_secret_must_be_long_enough(monkeypatch):
    monkeypatch.setenv('JWT_SECRET', 'too-short')
    with pytest.raises(SystemExit):
        server.load_jwt_secret()
    monkeypatch.delenv('JWT_SECRET')
    with pytest.raises(SystemExit):
        server.load_jwt_secret()
    monkeypatch.setenv('JWT_SECRET', SECRET)
    assert server.load_jwt_secret() == SECRET


def make_order(order_id=None, product='OLJCESPC7Z', qty=2):
    return demo_pb2.OrderResult(
        order_id=order_id or str(uuid.uuid4()),
        shipping_tracking_id='TR-123',
        shipping_cost=demo_pb2.Money(currency_code='EUR', units=8, nanos=990000000),
        shipping_address=demo_pb2.Address(street_address='1 Main St', city='Cluj', country='RO', zip_code=400000),
        items=[demo_pb2.OrderItem(
            item=demo_pb2.CartItem(product_id=product, quantity=qty),
            cost=demo_pb2.Money(currency_code='EUR', units=17, nanos=490000000))],
    )


def record(svc, user_id, order, units=43):
    return svc.RecordOrder(demo_pb2.RecordOrderRequest(
        user_id=user_id, order=order,
        total=demo_pb2.Money(currency_code='EUR', units=units, nanos=970000000)), FakeContext())


def test_record_and_list_orders_round_trip(svc):
    user_id = register(svc).user_id
    first, second = make_order(), make_order(product='66VCHSJNUP', qty=1)
    record(svc, user_id, first)
    record(svc, user_id, second, units=26)

    orders = svc.ListOrders(demo_pb2.ListOrdersRequest(user_id=user_id), FakeContext()).orders
    assert [o.order.order_id for o in orders] == [second.order_id, first.order_id]  # newest first
    assert orders[1].order == first  # the whole OrderResult survives storage
    assert (orders[1].total.currency_code, orders[1].total.units, orders[1].total.nanos) == ('EUR', 43, 970000000)
    assert orders[0].created_at_unix > orders[1].created_at_unix


def test_record_order_is_idempotent(svc, store):
    user_id = register(svc).user_id
    order = make_order()
    record(svc, user_id, order)
    record(svc, user_id, order)
    assert len(store.orders) == 1


def test_orders_are_per_user(svc):
    alice = register(svc).user_id
    bob = register(svc, email='bob@example.com', name='Bob').user_id
    record(svc, alice, make_order())
    assert svc.ListOrders(demo_pb2.ListOrdersRequest(user_id=bob), FakeContext()).orders == []


def test_record_order_rejects_bad_input(svc):
    user_id = register(svc).user_id
    with pytest.raises(Aborted) as e:
        record(svc, 'not-a-uuid', make_order())
    assert e.value.code == grpc.StatusCode.INVALID_ARGUMENT
    with pytest.raises(Aborted) as e:
        record(svc, user_id, make_order(order_id='nope'))
    assert e.value.code == grpc.StatusCode.INVALID_ARGUMENT
    with pytest.raises(Aborted) as e:
        svc.RecordOrder(demo_pb2.RecordOrderRequest(user_id=user_id, order=make_order()), FakeContext())
    assert e.value.code == grpc.StatusCode.INVALID_ARGUMENT
    with pytest.raises(Aborted) as e:
        record(svc, str(uuid.uuid4()), make_order())
    assert e.value.code == grpc.StatusCode.NOT_FOUND


def test_list_orders_rejects_bad_user_id(svc):
    with pytest.raises(Aborted) as e:
        svc.ListOrders(demo_pb2.ListOrdersRequest(user_id='x'), FakeContext())
    assert e.value.code == grpc.StatusCode.INVALID_ARGUMENT
