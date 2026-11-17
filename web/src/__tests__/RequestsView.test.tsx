import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { RequestsView } from '../views/RequestsView';
import { testRequests } from './testFixtures';

describe('RequestsView Component', () => {
  it('renders requests and allows filtering by status', async () => {
    const user = userEvent.setup({ delay: null });
    render(
      <RequestsView
        requests={testRequests}
        onApproveRequest={vi.fn()}
        onRejectRequest={vi.fn()}
        onSubmitRequest={vi.fn()}
        isAdminOrCurator={true}
      />
    );

    // Initial table has multiple requests
    expect(screen.getByText('Iron Sigil')).toBeInTheDocument();
    expect(screen.getByText('The Silent Star')).toBeInTheDocument();

    // Click on 'pending' filter tab
    const pendingFilterBtn = screen.getByTestId('filter-status-pending');
    await user.click(pendingFilterBtn);

    // Only 'Iron Sigil' should be visible
    expect(screen.getByText('Iron Sigil')).toBeInTheDocument();
    expect(screen.queryByText('The Silent Star')).not.toBeInTheDocument();
  });

  it('submits a new book request properly', async () => {
    const onSubmitRequestMock = vi.fn();
    const user = userEvent.setup({ delay: null });

    render(
      <RequestsView
        requests={testRequests}
        onApproveRequest={vi.fn()}
        onRejectRequest={vi.fn()}
        onSubmitRequest={onSubmitRequestMock}
        isAdminOrCurator={true}
      />
    );

    const titleInput = screen.getByTestId('request-title-input');
    const authorInput = screen.getByTestId('request-author-input');
    const submitBtn = screen.getByTestId('submit-request-btn');

    await user.type(titleInput, 'New Book');
    await user.type(authorInput, 'An Author');

    // Select audiobook format
    await user.click(screen.getByTestId('request-format-audiobook'));

    // Submit form
    await user.click(submitBtn);

    expect(onSubmitRequestMock).toHaveBeenCalledWith(
      'New Book',
      'An Author',
      'audiobook',
      undefined
    );
  });

  it('triggers onApproveRequest when curator clicks approve', async () => {
    const onApproveRequestMock = vi.fn();
    const user = userEvent.setup({ delay: null });

    render(
      <RequestsView
        requests={testRequests}
        onApproveRequest={onApproveRequestMock}
        onRejectRequest={vi.fn()}
        onSubmitRequest={vi.fn()}
        isAdminOrCurator={true}
      />
    );

    // Find approve button for req_401 (pending request 'Iron Sigil')
    const approveBtn = screen.getByTestId('approve-btn-req_401');
    expect(approveBtn).toBeInTheDocument();

    await user.click(approveBtn);
    expect(onApproveRequestMock).toHaveBeenCalledWith('req_401');
  });

  it('handles rejection flow with rejection reason modal', async () => {
    const onRejectRequestMock = vi.fn();
    const user = userEvent.setup({ delay: null });

    render(
      <RequestsView
        requests={testRequests}
        onApproveRequest={vi.fn()}
        onRejectRequest={onRejectRequestMock}
        onSubmitRequest={vi.fn()}
        isAdminOrCurator={true}
      />
    );

    // Click reject button on req_401
    const rejectBtn = screen.getByTestId('reject-btn-req_401');
    await user.click(rejectBtn);

    // Modal should now be visible
    expect(screen.getByText('Reject Book Request')).toBeInTheDocument();
    const reasonInput = screen.getByTestId('reject-reason-input');

    await user.type(reasonInput, 'Unavailable');

    // Confirm rejection
    const confirmBtn = screen.getByTestId('confirm-reject-btn');
    await user.click(confirmBtn);

    expect(onRejectRequestMock).toHaveBeenCalledWith(
      'req_401',
      'Unavailable'
    );

    // Modal should close
    expect(screen.queryByText('Reject Book Request')).not.toBeInTheDocument();
  });
});
